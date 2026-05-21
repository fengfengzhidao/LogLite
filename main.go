package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := application.New(application.Options{
		Name:        "LogLite",
		Description: "Lightweight local log viewer",
		Services: []application.Service{
			application.NewService(&LogService{}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "LogLite",
		Width:            1120,
		Height:           760,
		MinWidth:         920,
		MinHeight:        620,
		BackgroundColour: application.NewRGB(248, 250, 252),
		EnableFileDrop:   true,
		URL:              "/",
	})

	win.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		application.Get().Event.Emit("log-files-dropped", event.Context().DroppedFiles())
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
