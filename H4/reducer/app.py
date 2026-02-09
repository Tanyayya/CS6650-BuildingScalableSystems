import os
import json
from datetime import datetime, timezone
from typing import Dict, List

import boto3
from fastapi import FastAPI, HTTPException, Query

AWS_REGION = os.getenv("AWS_REGION", "us-east-1")
OUTPUT_BUCKET = os.getenv("OUTPUT_BUCKET")
OUTPUT_PREFIX = os.getenv("OUTPUT_PREFIX", "mapreduce")

if not OUTPUT_BUCKET:
    raise RuntimeError("Missing env var OUTPUT_BUCKET")

s3 = boto3.client("s3", region_name=AWS_REGION)
app = FastAPI(title="Reducer", version="1.0")


def parse_s3_url(s3_url: str):
    if not s3_url.startswith("s3://"):
        raise ValueError("URL must start with s3://")
    rest = s3_url[len("s3://"):]
    bucket, key = rest.split("/", 1)
    return bucket, key


@app.get("/health")
def health():
    return {"ok": True}


@app.get("/reduce")
def reduce_maps(
    job_id: str = Query(..., description="Job ID"),
    map_urls: List[str] = Query(..., description="List of mapper output S3 URLs"),
):
    final_counts: Dict[str, int] = {}

    # Aggregate mapper outputs
    for url in map_urls:
        try:
            bucket, key = parse_s3_url(url)
            obj = s3.get_object(Bucket=bucket, Key=key)
            data = json.loads(obj["Body"].read())
            counts = data["counts"]
        except Exception as e:
            raise HTTPException(
                status_code=500,
                detail=f"Failed to read {url}: {e}",
            )

        for word, count in counts.items():
            final_counts[word] = final_counts.get(word, 0) + count

    total_unique_words = len(final_counts)
    total_words = sum(final_counts.values())

    out_key = f"{OUTPUT_PREFIX}/{job_id}/reduce/final.json"

    payload = {
        "job_id": job_id,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "total_unique_words": total_unique_words,
        "total_words": total_words,
        "counts": final_counts,
    }

    try:
        s3.put_object(
            Bucket=OUTPUT_BUCKET,
            Key=out_key,
            Body=json.dumps(payload).encode("utf-8"),
            ContentType="application/json",
        )
    except Exception as e:
        raise HTTPException(
            status_code=500,
            detail=f"Failed to write reducer output to S3: {e}",
        )

    return {
        "output_url": f"s3://{OUTPUT_BUCKET}/{out_key}",
        "total_unique_words": total_unique_words,
        "total_words": total_words,
    }
