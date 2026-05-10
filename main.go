package main

import (
	"context"
	"embed"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"lokerwa/internal/api"
	"lokerwa/internal/hub"
	"lokerwa/internal/processor"
	"lokerwa/internal/storage"
	"lokerwa/internal/whatsapp"
)

//go:embed web
var webFS embed.FS

const (
	jobsDBPath = "./data/jobs.db"
	mediaDir   = "./data/media"
)

func main() {
	store, err := storage.New(jobsDBPath)
	if err != nil {
		log.Fatalf("storage init: %v", err)
	}

	proc, err := processor.New(store, mediaDir)
	if err != nil {
		log.Fatalf("processor init: %v", err)
	}

	h := hub.New()
	manager := whatsapp.NewManager()
	manager.OnJobEvt = proc.Handle

	handler := api.New(manager, h, store)

	go func() {
		if err := manager.Start(context.Background()); err != nil {
			log.Printf("WhatsApp start error: %v", err)
		}
	}()

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.GET("/", func(c *gin.Context) {
		data, _ := webFS.ReadFile("web/index.html")
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	})
	r.GET("/board", func(c *gin.Context) {
		data, _ := webFS.ReadFile("web/board.html")
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	})
	r.GET("/api-test", func(c *gin.Context) {
		data, _ := webFS.ReadFile("web/api.html")
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	})

	handler.Register(r, mediaDir)

	log.Println("Server running on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
