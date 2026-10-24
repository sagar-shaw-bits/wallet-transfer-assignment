package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-transfer-assignment/internal/model"
	"wallet-transfer-assignment/internal/service"
)

type Handler struct {
	service service.TransferService
}

func NewHandler(svc service.TransferService) *Handler {
	return &Handler{service: svc}
}

func NewRouter(svc service.TransferService) *gin.Engine {
	r := gin.Default()
	h := NewHandler(svc)

	r.POST("/transfers", h.CreateTransfer)
	return r
}

func (h *Handler) CreateTransfer(c *gin.Context) {
	var req model.TransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "request body is invalid",
		})
		return
	}

	result, err := h.service.CreateTransfer(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrInsufficientFunds):
			c.JSON(http.StatusConflict, result)
			return
		case errors.Is(err, model.ErrWalletNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "wallet_not_found", "message": err.Error()})
			return
		case errors.Is(err, model.ErrInvalidRequest):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
			return
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "an unexpected error occurred"})
			return
		}
	}

	if result.IsFailed() {
		c.JSON(http.StatusConflict, result)
		return
	}
	if result.Replayed {
		c.JSON(http.StatusOK, result)
		return
	}

	c.JSON(http.StatusCreated, result)
}
