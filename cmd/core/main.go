package main

import (
	"context"
	"encoding/json"
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
	"github.com/e7217/edg/internal/httpapi"
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
	migrateDownFlag := flag.Int("migrate-down", 0, "Rollback metadata DB by N migration steps and exit")
	checkConstraintsFlag := flag.Bool("check-constraints", false, "Check catalog template constraints and exit")
	configFlag := flag.String("config", os.Getenv("EDG_CORE_CONFIG"), "Path to core configuration file")
	flag.Parse()

	// Handle version flag
	if *versionFlag {
		fmt.Printf("EDG Platform Core\n")
		fmt.Printf("Version:    %s\n", Version)
		fmt.Printf("Build Time: %s\n", BuildTime)
		fmt.Printf("Git Commit: %s\n", GitCommit)
		os.Exit(0)
	}

	configPath := *configFlag
	if configPath == "" {
		configPath = discoverConfigPath()
	}

	cfg, err := core.LoadCoreConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load core config: %v", err)
	}

	// Allow the VM sink endpoint to be overridden without editing the config
	// file (e.g. to point at a service hostname in container deployments).
	if sinkURL := os.Getenv("EDG_SINK_URL"); sinkURL != "" {
		cfg.Sink.URL = sinkURL
	}

	if *migrateDownFlag > 0 {
		if err := core.RunMigrationSteps(cfg.Storage.MetadataDB, -*migrateDownFlag); err != nil {
			log.Fatalf("Failed to rollback metadata DB: %v", err)
		}
		log.Printf("[Core] Rolled back metadata DB by %d migration step(s)", *migrateDownFlag)
		os.Exit(0)
	}

	if *checkConstraintsFlag {
		report, err := checkConstraints(cfg)
		if err != nil {
			log.Fatalf("Failed to check constraints: %v", err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			log.Fatalf("Failed to write constraints report: %v", err)
		}
		if report.ViolationCount > 0 {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// 1. Embedded NATS Server configuration
	opts := &server.Options{
		Port:      cfg.NATS.Port,
		HTTPPort:  cfg.NATS.HTTPPort, // for monitoring
		JetStream: true,              // Enable JetStream for message persistence
		StoreDir:  cfg.NATS.StoreDir,
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
	log.Printf("  NATS: nats://localhost:%d", cfg.NATS.Port)
	log.Printf("  Monitor: http://localhost:%d", cfg.NATS.HTTPPort)
	log.Println("=================================")

	// 3. Connect as internal client
	nc, err := nats.Connect(fmt.Sprintf("nats://localhost:%d", cfg.NATS.Port))
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	// 3.1. Initialize JetStream context
	js, err := nc.JetStream()
	if err != nil {
		log.Fatalf("Failed to create JetStream context: %v", err)
	}

	// 3.2. Create or align JetStream stream for platform data
	streamConfig, err := cfg.JetStream.Stream.NATSConfig()
	if err != nil {
		log.Fatalf("Invalid JetStream stream config: %v", err)
	}

	_, err = js.StreamInfo(streamConfig.Name)
	if err != nil {
		// Stream doesn't exist, create it
		_, err = js.AddStream(streamConfig)
		if err != nil {
			log.Fatalf("Failed to create JetStream stream: %v", err)
		}
		log.Printf("[Core] Created JetStream stream: %s", streamConfig.Name)
	} else {
		if _, err := js.UpdateStream(streamConfig); err != nil {
			log.Fatalf("Failed to update JetStream stream: %v", err)
		}
		log.Printf("[Core] JetStream stream aligned: %s", streamConfig.Name)
	}

	// 4. Initialize metadata store
	store, err := core.NewStoreWithMigrations(cfg.Storage.MetadataDB, cfg.Storage.AutoMigrate)
	if err != nil {
		log.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// 5. Initialize template loader
	loader := core.NewTemplateLoader()
	if err := loader.LoadFromDir(cfg.Templates.Dir); err != nil {
		log.Printf("[Core] Warning: Failed to load templates: %v", err)
	}
	log.Printf("[Core] Loaded %d templates", loader.Count())

	// 6. Create handlers and subscribe
	eventPublisher := core.NewEventPublisher(nc)
	enricher := core.NewEnricher(store, core.EnricherOptions{
		MaxDepth: core.DefaultTraversalMaxDepth,
	})
	if err := enricher.Start(nc); err != nil {
		log.Fatalf("Failed to start enricher: %v", err)
	}
	defer enricher.Stop()

	dataHandler := core.NewDataHandlerWithConfig(js, store, core.DataHandlerOptions{
		ValidatedSubject:   cfg.JetStream.ValidatedSubject,
		DeadLetterSubject:  cfg.JetStream.DeadLetterSubject,
		Events:             eventPublisher,
		UnknownAssetPolicy: cfg.UnknownAssetPolicy,
		Enricher:           enricher,
	})
	metaHandler := core.NewMetaHandlerWithOptions(store, loader, core.MetaHandlerOptions{
		Events:                eventPublisher,
		ConstraintEnforcement: cfg.Constraints.Enforcement,
	})
	metaService := core.NewMetadataService(store, loader, eventPublisher, cfg.Constraints.Enforcement)
	alarmHandler := core.NewAlarmHandler(store, eventPublisher, core.AlarmHandlerOptions{
		Window:            time.Duration(cfg.Alarm.WindowSeconds) * time.Second,
		MaxTraversalDepth: cfg.Alarm.MaxTraversalDepth,
	})
	serviceCtx, stopServices := context.WithCancel(context.Background())
	defer stopServices()

	// Built-in VictoriaMetrics sink: a durable JetStream pull consumer that
	// replaces the external Telegraf bridge (see ADR 0005).
	var sink *core.VMSink
	if cfg.Sink.Enabled {
		sink, err = core.NewVMSink(js, cfg.JetStream.ValidatedSubject, cfg.Sink)
		if err != nil {
			log.Fatalf("Failed to create VM sink: %v", err)
		}
		if err := sink.Start(serviceCtx); err != nil {
			log.Fatalf("Failed to start VM sink: %v", err)
		}
		log.Printf("[Core] VM sink writing %q to %s", cfg.JetStream.ValidatedSubject, cfg.Sink.URL)
	}

	if cfg.HTTP.Enabled {
		httpServer := httpapi.NewServer(store, metaService, httpapi.Options{
			Address:            cfg.HTTP.Address,
			TokenEnv:           cfg.HTTP.TokenEnv,
			CORSAllowedOrigins: cfg.HTTP.CORSAllowedOrigins,
			Version:            Version,
			BuildTime:          BuildTime,
			GitCommit:          GitCommit,
		})
		go func() {
			log.Printf("[Core] HTTP API listening on http://%s", cfg.HTTP.Address)
			if err := httpServer.Run(serviceCtx); err != nil {
				log.Printf("[Core] HTTP API stopped: %v", err)
			}
		}()
	}

	_, err = nc.Subscribe("platform.data.asset", dataHandler.HandleAssetData)
	if err != nil {
		log.Fatalf("Failed to subscribe: %v", err)
	}

	if err := metaHandler.RegisterHandlers(nc); err != nil {
		log.Fatalf("Failed to register meta handlers: %v", err)
	}
	if err := alarmHandler.RegisterHandlers(nc); err != nil {
		log.Fatalf("Failed to register alarm handlers: %v", err)
	}

	log.Println("[Core] Subscribed to: platform.data.asset")

	// 7. Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[Core] Shutting down...")
	stopServices()
	if sink != nil {
		sink.Stop()
	}
	nc.Drain()
	ns.Shutdown()
}

func checkConstraints(cfg core.CoreConfig) (core.ConstraintsReport, error) {
	store, err := core.NewStoreWithMigrations(cfg.Storage.MetadataDB, cfg.Storage.AutoMigrate)
	if err != nil {
		return core.ConstraintsReport{}, err
	}
	defer store.Close()

	loader := core.NewTemplateLoader()
	if err := loader.LoadFromDir(cfg.Templates.Dir); err != nil {
		return core.ConstraintsReport{}, err
	}

	return core.NewConstraintsEvaluator(loader).CheckAll(store)
}

func discoverConfigPath() string {
	for _, path := range []string{
		"/opt/edg/config.yaml",
		"deploy/configs/core/config.dev.yaml",
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}
