#!/bin/bash
# EDG IoT Platform - Local Test Pipeline

set -e

echo "================================================"
echo "  EDG IoT Platform - Local Test Pipeline"
echo "================================================"
echo ""

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Step 1: Build EDG Core
echo -e "${BLUE}[1/4] Building EDG Core...${NC}"
go build -o edg-core ./cmd/core
echo -e "${GREEN}✓ EDG Core built${NC}"
echo ""

# Step 2: Check/Install VictoriaMetrics
echo -e "${BLUE}[2/4] Checking VictoriaMetrics...${NC}"
if ! command -v victoria-metrics &> /dev/null; then
    echo "VictoriaMetrics not found. Downloading..."
    VM_VERSION="v1.96.0"
    ARCH=$(uname -m)
    if [ "$ARCH" = "x86_64" ]; then
        VM_ARCH="amd64"
    elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
        VM_ARCH="arm64"
    else
        echo "Unsupported architecture: $ARCH"
        exit 1
    fi

    wget -q "https://github.com/VictoriaMetrics/VictoriaMetrics/releases/download/${VM_VERSION}/victoria-metrics-linux-${VM_ARCH}-${VM_VERSION}.tar.gz"
    tar xzf victoria-metrics-linux-${VM_ARCH}-${VM_VERSION}.tar.gz
    rm victoria-metrics-linux-${VM_ARCH}-${VM_VERSION}.tar.gz
    VM_BIN="./victoria-metrics-prod"
else
    VM_BIN="victoria-metrics"
fi
echo -e "${GREEN}✓ VictoriaMetrics ready${NC}"
echo ""

# Step 3: Check/Install Telegraf
echo -e "${BLUE}[3/4] Checking Telegraf...${NC}"
if ! command -v telegraf &> /dev/null; then
    echo "Telegraf not found. Downloading..."
    TELEGRAF_VERSION="1.29.0"
    ARCH=$(uname -m)
    if [ "$ARCH" = "x86_64" ]; then
        TELEGRAF_ARCH="amd64"
    elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
        TELEGRAF_ARCH="arm64"
    else
        echo "Unsupported architecture: $ARCH"
        exit 1
    fi

    wget -q "https://dl.influxdata.com/telegraf/releases/telegraf-${TELEGRAF_VERSION}_linux_${TELEGRAF_ARCH}.tar.gz"
    tar xzf "telegraf-${TELEGRAF_VERSION}_linux_${TELEGRAF_ARCH}.tar.gz"
    rm "telegraf-${TELEGRAF_VERSION}_linux_${TELEGRAF_ARCH}.tar.gz"
    TELEGRAF_BIN="./telegraf-${TELEGRAF_VERSION}/usr/bin/telegraf"
else
    TELEGRAF_BIN="telegraf"
fi
echo -e "${GREEN}✓ Telegraf ready${NC}"
echo ""

# Step 4: Start services
echo -e "${BLUE}[4/4] Starting services...${NC}"
echo ""

# Start VictoriaMetrics
echo -e "${YELLOW}Starting VictoriaMetrics on :8428...${NC}"
$VM_BIN -storageDataPath=./victoria-metrics-data -retentionPeriod=1 > /tmp/victoria-metrics.log 2>&1 &
VM_PID=$!
sleep 2

# Start EDG Core
echo -e "${YELLOW}Starting EDG Core on :4222 (NATS) and :8222 (Monitor)...${NC}"
./edg-core > /tmp/edg-core.log 2>&1 &
CORE_PID=$!
sleep 2

# Start Telegraf
echo -e "${YELLOW}Starting Telegraf...${NC}"
$TELEGRAF_BIN --config ./configs/telegraf/telegraf.conf > /tmp/telegraf.log 2>&1 &
TELEGRAF_PID=$!
sleep 2

echo ""
echo -e "${GREEN}✓ All services started${NC}"
echo ""
echo "================================================"
echo "  Service Status"
echo "================================================"
echo "  EDG Core PID:        $CORE_PID"
echo "  Telegraf PID:        $TELEGRAF_PID"
echo "  VictoriaMetrics PID: $VM_PID"
echo ""
echo "  NATS:            http://localhost:4222"
echo "  NATS Monitor:    http://localhost:8222"
echo "  VictoriaMetrics: http://localhost:8428"
echo ""
echo "================================================"
echo "  Test Data Publishing"
echo "================================================"
echo ""

# Create test publisher
cat > /tmp/test_data.go <<'EOF'
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

type TestData struct {
	AssetID string      `json:"asset_id"`
	Values  []TestValue `json:"values"`
}

type TestValue struct {
	Name    string   `json:"name"`
	Number  *float64 `json:"number,omitempty"`
	Unit    string   `json:"unit,omitempty"`
	Quality string   `json:"quality"`
}

func main() {
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer nc.Close()

	for i := 0; i < 5; i++ {
		temp := 20.0 + float64(i)*2.0
		humidity := 50.0 + float64(i)*5.0

		testData := TestData{
			AssetID: fmt.Sprintf("sensor-%03d", i+1),
			Values: []TestValue{
				{
					Name:    "temperature",
					Number:  &temp,
					Unit:    "°C",
					Quality: "good",
				},
				{
					Name:    "humidity",
					Number:  &humidity,
					Unit:    "%",
					Quality: "good",
				},
			},
		}

		data, _ := json.Marshal(testData)
		nc.Publish("platform.data.asset", data)

		fmt.Printf("Published data for sensor-%03d (temp=%.1f°C, humidity=%.1f%%)\n",
			i+1, temp, humidity)
		time.Sleep(1 * time.Second)
	}

	fmt.Println("\nWaiting for data to be processed (3 seconds)...")
	time.Sleep(3 * time.Second)
}
EOF

# Publish test data
echo "Publishing 5 test sensor readings..."
go run /tmp/test_data.go

echo ""
echo "================================================"
echo "  Verification"
echo "================================================"
echo ""

# Check VictoriaMetrics
echo -e "${YELLOW}Querying VictoriaMetrics...${NC}"
sleep 2

QUERY_RESULT=$(curl -s "http://localhost:8428/api/v1/query?query=temperature" | grep -o '"status":"success"' || echo "")

if [ -n "$QUERY_RESULT" ]; then
    echo -e "${GREEN}✓ VictoriaMetrics is receiving data${NC}"
    echo ""
    echo "Sample query:"
    curl -s "http://localhost:8428/api/v1/query?query=temperature" | jq '.'
else
    echo -e "${YELLOW}⚠ No data found yet (might need more time)${NC}"
fi

echo ""
echo "================================================"
echo "  Logs"
echo "================================================"
echo ""

echo -e "${YELLOW}EDG Core logs:${NC}"
tail -10 /tmp/edg-core.log

echo ""
echo -e "${YELLOW}Telegraf logs:${NC}"
tail -10 /tmp/telegraf.log

echo ""
echo "================================================"
echo "  Cleanup"
echo "================================================"
echo ""
echo "To stop all services:"
echo "  kill $CORE_PID $TELEGRAF_PID $VM_PID"
echo ""
echo "To view live logs:"
echo "  tail -f /tmp/edg-core.log"
echo "  tail -f /tmp/telegraf.log"
echo "  tail -f /tmp/victoria-metrics.log"
echo ""
echo "To query data from VictoriaMetrics:"
echo "  curl 'http://localhost:8428/api/v1/query?query=temperature'"
echo "  curl 'http://localhost:8428/api/v1/query?query=humidity'"
echo ""

# Save PIDs for easy cleanup
echo "$CORE_PID $TELEGRAF_PID $VM_PID" > /tmp/edg-pids.txt

echo "PIDs saved to /tmp/edg-pids.txt"
echo ""
echo -e "${GREEN}Test pipeline completed!${NC}"
