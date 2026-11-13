package server_handler

import (
	"github.com/gin-gonic/gin"
	"go.mau.fi/whatsmeow/store"
)

type ServerHandler interface {
	ServerOk(ctx *gin.Context)
}

type serverHandler struct {
}

// ServerOk implements ServerHandler.
func (s *serverHandler) ServerOk(ctx *gin.Context) {
	// Lander patch: expose the WhatsApp client version used in the handshake,
	// so monitoring can tell which build is running when a "405 client
	// outdated" happens. No secret here.
	ctx.JSON(200, gin.H{
		"status":    "ok",
		"waVersion": store.GetWAVersion().String(),
	})
}

func NewServerHandler() ServerHandler {
	return &serverHandler{}
}
