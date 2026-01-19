package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/e7217/edg/internal/core"
)

var (
	// Version information (injected at build time via -ldflags)
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

func main() {
	// Parse command-line flags
	versionFlag := flag.Bool("version", false, "Print version information and exit")
	flag.Parse()

	// Handle version flag
	if *versionFlag {
		fmt.Printf("EDG Platform Core\n")
		fmt.Printf("Version:    %s\n", Version)
		fmt.Printf("Build Time: %s\n", BuildTime)
		fmt.Printf("Git Commit: %s\n", GitCommit)
		os.Exit(0)
	}
	// 1. Embedded NATS Server configuration
	opts := &server.Options{
		Port:      4222,
		HTTPPort:  8222, // for monitoring
		JetStream: true, // Enable JetStream for message persistence
		StoreDir:  "./data/jetstream",
	}

	ns, err := server.NewServer(opts)
	if err != nil {
		log.Fatalf("Failed to create NATS server: %v", err)
	}

	// 2. Start NATS Server (async)
	go ns.Start()

	// Wait for server ready
	if !ns.ReadyForConnections(5 * time.Second) {
		log.Fatal("NATS server not ready")
	}

	log.Println("=================================")
	log.Println("  EDG Platform Core Started")
	log.Println("  NATS: nats://localhost:4222")
	log.Println("  Monitor: http://localhost:8222")
	log.Println("=================================")

	// 3. Connect as internal client
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	// 3.1. Initialize JetStream context
	js, err := nc.JetStream()
	if err != nil {
		log.Fatalf("Failed to create JetStream context: %v", err)
	}

	// 3.2. Create JetStream stream for platform data
	streamName := "PLATFORM_DATA"
	_, err = js.StreamInfo(streamName)
	if err != nil {
		// Stream doesn't exist, create it
		_, err = js.AddStream(&nats.StreamConfig{
			Name:     streamName,
			Subjects: []string{"platform.data.>"},
			Storage:  nats.FileStorage,
			MaxAge:   7 * 24 * time.Hour, // 7 days retention
		})
		if err != nil {
			log.Fatalf("Failed to create JetStream stream: %v", err)
		}
		log.Printf("[Core] Created JetStream stream: %s", streamName)
	} else {
		log.Printf("[Core] JetStream stream already exists: %s", streamName)
	}

	// 4. Initialize metadata store
	store, err := core.NewStore("./data/metadata.db")
	if err != nil {
		log.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// 5. Initialize template loader
	loader := core.NewTemplateLoader()
	if err := loader.LoadFromDir("./templates"); err != nil {
		log.Printf("[Core] Warning: Failed to load templates: %v", err)
	}
	log.Printf("[Core] Loaded %d templates", loader.Count())

	// 6. Create handlers and subscribe
	dataHandler := core.NewDataHandler(js, store)
	metaHandler := core.NewMetaHandler(store, loader)

	_, err = nc.Subscribe("platform.data.asset", dataHandler.HandleAssetData)
	if err != nil {
		log.Fatalf("Failed to subscribe: %v", err)
	}

	if err := metaHandler.RegisterHandlers(nc); err != nil {
		log.Fatalf("Failed to register meta handlers: %v", err)
	}

	log.Println("[Core] Subscribed to: platform.data.asset")

	// 7. Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[Core] Shutting down...")
	nc.Drain()
	ns.Shutdown()
}
