# Phase 1 — Synchronous Order Processing

> **Goal:** Experience first-hand why synchronous systems fail under flash-sale load,
> then understand *exactly* what breaks and why.

---

## Project Structure

```
ecommerce-lab/
├── cmd/server/main.go                  # HTTP server entrypoint
├── internal/
│   ├── handler/orders.go               # POST /orders/sync handler
│   ├── model/order.go                  # Order, Item, response types
│   └── worker/payment.go               # Buffered-channel payment processor
├── deployments/
│   ├── docker/
│   │   ├── Dockerfile                  # Multi-stage Go build → scratch image
│   │   └── docker-compose.yml          # Local stack: server + LocalStack + Locust
│   ├── locust/
│   │   └── locustfile.py               # Load test scenarios
│   └── terraform/
│       └── main.tf                     # VPC + ECS + ALB (lab spec)
└── README.md
```

---

## The Core Concept — Why `time.Sleep` Doesn't Work

When a goroutine calls `time.Sleep(3s)`, the Go runtime **parks** the goroutine
and frees the OS thread. That means 10,000 goroutines can all "sleep" concurrently
with zero throughput penalty — a terrible model for a real payment processor.

Instead, we use a **buffered channel as a semaphore**:

```go
// Create a channel with capacity = max concurrent payments
sem := make(chan struct{}, MaxConcurrentPayments)  // cap = 5

// Acquire a slot (BLOCKS when all 5 slots are taken)
sem <- struct{}{}

// Do the work...
time.Sleep(PaymentVerificationDelay)

// Release the slot
<-sem
```

**Key difference:** When all 5 slots are occupied, the 6th goroutine _blocks
on the channel send_. It cannot proceed. This is a real throughput wall —
not a fake sleep that lets everything through.

```
Capacity math:
  5 slots × (1 verification / 3 seconds) = ~1.67 completions/slot/sec
  Total system throughput ceiling ≈ 8.3 orders/sec
  
Normal ops:   5 users × 1 req / 0.1–0.5s = ~10–50 req/s  ← exceeds cap slightly
Flash sale:  20 users × 1 req / 0.1–0.5s = ~40–200 req/s ← far exceeds cap
```

---

## Quick Start — Local Development

### Prerequisites

- Docker + Docker Compose
- Go 1.22+ (for running tests)
- Python 3.11+ + `locust` (for load tests)

### 1. Start the server

```bash
cd deployments/docker
docker compose up order-service
```

The server is now at `http://localhost:8080`.

```bash
# Verify it's running
curl http://localhost:8080/health
# {"status":"ok"}
```

### 2. Send a test order

```bash
curl -s -X POST http://localhost:8080/orders/sync \
  -H "Content-Type: application/json" \
  -d '{
    "customer_id": 42,
    "status": "pending",
    "items": [
      {"product_id": "SHOE-001", "name": "Trail Runner X", "quantity": 1, "price": 129.99}
    ]
  }' | jq .
```

You'll wait **~3 seconds** before seeing:

```json
{
  "order_id": "550e8400-...",
  "status": "completed",
  "message": "Order processed and payment verified",
  "latency_ms": "3.001s"
}
```

### 3. Check payment processor metrics

```bash
curl -s http://localhost:8080/metrics/payment | jq .
```

---

## Load Testing with Locust

### Install Locust

```bash
pip install locust
```

### Test 1 — Normal Operations (5 users, 30 seconds)

```bash
locust -f deployments/locust/locustfile.py \
  --host http://localhost:8080 \
  --headless \
  -u 5 -r 1 \
  --run-time 30s \
  --html reports/normal_ops.html \
  --csv  reports/normal_ops
```

**Expected result:**
- Success rate: ~98–100%
- Median response time: ~3,000ms (payment verification)
- 95th percentile: ~3,200ms
- Throughput: ~1–2 req/s (limited by the 3s wait per user)

### Test 2 — Flash Sale (20 users, 60 seconds)

```bash
locust -f deployments/locust/locustfile.py \
  --host http://localhost:8080 \
  --headless \
  -u 20 -r 10 \
  --run-time 60s \
  --html reports/flash_sale.html \
  --csv  reports/flash_sale
```

**Expected result:**
- Success rate: degrades over time as goroutines pile up
- Median response time: climbs from 3s → 10s → 30s+
- 95th percentile: may exceed server write timeout (60s)
- Failures: connection timeouts, 500s, goroutine leak warnings in server logs

### Test 3 — Web UI Mode (watch it in real time)

```bash
locust -f deployments/locust/locustfile.py --host http://localhost:8080
# Open http://localhost:8089
# Set users=20, spawn rate=10, then click Start
```

---

## The Failure Mode, Explained

Under flash sale load, here is the exact sequence of events:

```
t=0s    20 users start sending orders
t=0s    5 goroutines acquire payment processor slots
t=0s    15+ goroutines block on: sem <- struct{}{}
t=3s    First 5 complete, release slots
t=3s    Next 5 goroutines unblock and start processing
t=3s    ~55 goroutines are now queued behind the semaphore
        (20 users × 1 req / 300ms avg wait = ~66 req queued after 20s)
t=20s   HTTP connections start timing out on the client side
        ALB returns 504 Gateway Timeout
t=60s   Server goroutine count: potentially 100s
        Memory pressure, GC pauses, log flooding
```

**What your customers see:**
- First 30s: Very slow checkouts (~10–30s to get a response)
- After 30s: Hard errors (504, connection refused)
- Conversion rate: ~10% of normal

---

## AWS Deployment

### Push image to ECR

```bash
# Create the repository
aws ecr create-repository --repository-name ecommerce-lab

# Authenticate and push
aws ecr get-login-password | docker login --username AWS \
  --password-stdin <account>.dkr.ecr.us-east-1.amazonaws.com

docker build -f deployments/docker/Dockerfile -t ecommerce-lab .
docker tag ecommerce-lab:latest <account>.dkr.ecr.us-east-1.amazonaws.com/ecommerce-lab:latest
docker push <account>.dkr.ecr.us-east-1.amazonaws.com/ecommerce-lab:latest
```

### Deploy with Terraform

```bash
cd deployments/terraform
terraform init
terraform apply -var="image_uri=<account>.dkr.ecr.us-east-1.amazonaws.com/ecommerce-lab:latest"
```

Terraform provisions:
- VPC `10.0.0.0/16` with public + private subnets (lab spec)
- NAT Gateway for private subnet egress
- Application Load Balancer in public subnets
- ECS Fargate service: 256 CPU / 512 MB (lab spec)
- CloudWatch log group

```bash
# Get your ALB endpoint
terraform output alb_dns_name

# Run Locust against the real AWS deployment
locust -f deployments/locust/locustfile.py \
  --host http://$(terraform output -raw alb_dns_name) \
  --headless -u 20 -r 10 --run-time 60s
```

---

## What To Observe

| Metric               | Normal Ops (5u) | Flash Sale (20u) |
|----------------------|-----------------|------------------|
| Throughput (req/s)   | ~1–2            | Plateaus at ~1.6 |
| Median latency       | ~3,000ms        | 3,000 → 30,000ms |
| 95th percentile      | ~3,200ms        | 30,000ms+        |
| Success rate         | ~99%            | Drops below 50%  |
| Server goroutines    | ~10             | 100s → OOM risk  |

**The key insight:** The server _looks_ like it's handling the load because
Go spawns goroutines for every connection. But they're all blocked waiting
for a payment slot. The bottleneck is invisible until you measure it.

---

## Phase 2 Preview

Phase 2 replaces the synchronous handler with:

```
POST /orders/async
  → Publish to SNS topic        (< 5ms)
  → Return 202 Accepted         (immediate)
  
SNS → SQS queue
  → Worker polls SQS
  → Worker calls VerifyPayment  (still 3s, but decoupled)
  → Worker updates order status
```

The acceptance rate becomes **100% regardless of load**. Workers scale
independently from the web tier. The queue absorbs flash-sale spikes.

---

## Endpoints Reference

| Method | Path                | Description                        |
|--------|---------------------|------------------------------------|
| `GET`  | `/health`           | ALB health check → 200 OK          |
| `POST` | `/orders/sync`      | Phase 1: synchronous processing    |
| `GET`  | `/metrics/payment`  | Live payment processor stats       |
