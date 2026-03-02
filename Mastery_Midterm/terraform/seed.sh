#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://<PUBLIC_IP>:8080}"

for i in 1 2; do
  curl -s -o /dev/null -w "%{http_code}\n" \
    -X POST "$BASE_URL/v1/products/$i/details" \
    -H "Content-Type: application/json" \
    -d "{
      \"product_id\": $i,
      \"sku\": \"SKU-$i\",
      \"manufacturer\": \"Acme Corporation\",
      \"category_id\": 456,
      \"weight\": 1300,
      \"some_other_id\": 789
    }"

done
