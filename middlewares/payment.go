package middlewares

import (
	"caaso/models"
	"caaso/services"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mercadopago/sdk-go/pkg/config"
	"github.com/mercadopago/sdk-go/pkg/payment"
)

// ConfirmPayment is your generic /go/payment/confirm endpoint.
// It re-fetches the payment from MP (to guarantee it really is approved),
// then reads the metadata (userId & planType) you originally set.
func CheckPayment(c *gin.Context) {
	eventType := c.DefaultQuery("topic", c.Query("type"))
	if eventType != "payment" {
		c.AbortWithStatus(http.StatusNoContent)
		return
	}

	idStr := c.DefaultQuery("id", c.Query("data.id"))
	paymentID, err := strconv.Atoi(idStr)
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// Initialize MP SDK client
	cfg, err := config.New(services.AccessToken)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	client := payment.NewClient(cfg)

	// Re-fetch the payment from Mercado Pago
	mpResp, err := client.Get(context.Background(), paymentID)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	fmt.Println(mpResp.Status)
	// Confirm it really is approved
	if mpResp.Status != "approved" {
		c.AbortWithStatus(http.StatusOK)
		return
	}

	// Pull metadata back out
	userId, _ := mpResp.Metadata["user_id"].(string)
	planType, _ := mpResp.Metadata["plan_type"].(string)

	// Check if there is another payment that is not the payment that the user paid
	var existing models.Payment
	if err := services.DB.
		Where("user_id = ?", userId).
		First(&existing).
		Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
	}

	// Se encontrar um pagamento diferente do atual, reembolsa
	// Se não houver nenhum registro, também reembolsa
	if existing.ID == 0 || existing.ID != paymentID {
		resp, err := services.RefundWithRetry(paymentID)
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}

		fmt.Printf("✅ Payment %d refunded → refund id=%d status=%s\n",
			paymentID, resp.ID, resp.Status,
		)

		c.AbortWithStatus(http.StatusOK)
		return

	}

	// Update payment row
	updates := map[string]any{
		"is_paid":       true,
		"date_approved": time.Now(),
	}
	if err := services.DB.
		Model(&models.Payment{}).
		Where("id = ?", paymentID).
		Updates(updates).
		Error; err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// Load the user into context and pass to next middleware
	c.Set("currentUserId", userId)
	c.Set("planType", planType)
	c.Next()
}
