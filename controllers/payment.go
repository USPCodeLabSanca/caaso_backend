package controllers

import (
	"caaso/models"
	"caaso/services"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type Plan struct {
	UserType string  `json:"userType"`
	PlanType string  `json:"planType"`
	Amount   float64 `json:"amount"`
}

var amounts = map[string]map[string]float64{
	models.Aloja.String(): {
		models.Monthly.String(): 2.00,
		models.Yearly.String():  20.00,
	},
	models.Grad.String(): {
		models.Monthly.String(): 5.00,
		models.Yearly.String():  45.00,
	},
	models.PostGrad.String(): {
		models.Monthly.String(): 8.00,
		models.Yearly.String():  70.00,
	},
	models.Other.String(): {
		models.Monthly.String(): 15.00,
		models.Yearly.String():  135.00,
	},
}

func GetPlans(c *gin.Context) {
	var list []Plan
	for u, plans := range amounts {
		for p, amt := range plans {
			list = append(list, Plan{
				UserType: u,
				PlanType: p,
				Amount:   amt,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Planos carregados", "plans": list})

}

func CreatePayment(c *gin.Context) {

	user, _ := c.Get("currentUser")
	User := user.(models.User)

	if User.IsSubscribed {
		c.JSON(http.StatusForbidden, gin.H{"message": "Você já possui um plano ativo"})
		return
	}

	var input models.PaymentInput

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if !strings.Contains(strings.Split(User.Email, "@")[1], "usp.br") {
		if input.UserType != models.Other.String() {
			c.JSON(http.StatusForbidden, gin.H{"message": "Você precisa estar logado com o e-mail @usp.br para escolher esse tipo de usuário"})
			return
		}
	}

	// Check if the user is a resident of alojamento
	if input.UserType == models.Aloja.String() {
		var alojaUser models.AlojaUser
		if err := services.DB.
			Model(&alojaUser).
			Where("email = ?", User.Email).
			First(&alojaUser).
			Error; err != nil {
			c.JSON(http.StatusForbidden, gin.H{"message": "Você não está cadastrado como morador do alojamento. Contate o CAASO caso tenha dúvidas"})
			return
		}
	}

	// Update the User.type column:
	if err := services.DB.
		Model(&User).
		Where("id = ?", User.ID).
		Update("type", input.UserType).
		Error; err != nil {

		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	amount := amounts[input.UserType][input.PlanType]

	resource, err := services.CreatePayment(amount, User.Email, User.ID, input.PlanType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	// remove any previous payments for this user
	if err := services.DB.
		Where("user_id = ?", User.ID).
		Delete(&models.Payment{}).
		Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	// Create new payment
	var payment = models.Payment{
		ID:     resource.ID,
		UserId: User.ID,
		QrCode: resource.PointOfInteraction.TransactionData.QRCode,
		Amount: resource.TransactionAmount,
	}

	result := services.DB.Create(&payment)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": result.Error.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Pagamento criado com sucesso", "payment": payment})

}

func GetPayment(c *gin.Context) {

	user, _ := c.Get("currentUser")
	User := user.(models.User)

	var payment models.Payment
	result := services.DB.Where("user_id=?", User.ID).First(&payment)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": result.Error.Error()})
		return
	}

	resource, err := services.GetPaymentFromId(payment.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	switch status := resource.Status; status {
	case "in_process":
		c.JSON(http.StatusForbidden, gin.H{"message": "O seu pagamento está em processamento"})
	case "cancelled", "rejected":
		result := services.DB.Delete(&payment)
		if result.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"message": result.Error.Error()})
			return
		}
		c.JSON(http.StatusForbidden, gin.H{"message": "O seu pagamento foi cancelado. Recarregue e tente novamente"})
	case "pending":
		// pagamento com "isPaid" false
		c.JSON(http.StatusOK, gin.H{"message": "Pagamento pendente", "payment": payment})
	case "approved":
		// pagamento com "isPaid" true
		result := services.DB.Delete(&payment)
		if result.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"message": result.Error.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Pagamento aprovado", "payment": payment})
		return
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"message": "Status desconhecido."})
		return
	}
}
