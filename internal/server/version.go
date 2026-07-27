package server

// Version is the application version reported by /health and the UI.
// It is overridden at build time with:
//
//	go build -ldflags="-X github.com/dtorcivia/schedlock/internal/server.Version=v1.2.3"
var Version = "dev"
