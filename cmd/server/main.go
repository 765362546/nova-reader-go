package main

import (
	"log"

	"github.com/765362546/novel-reader-go/internal/config"
	"github.com/765362546/novel-reader-go/internal/httpapi"
)

func main() {
	app := config.Load()
	if err := httpapi.New(app).Run(); err != nil {
		log.Fatal(err)
	}
}
