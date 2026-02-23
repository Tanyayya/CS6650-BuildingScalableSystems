import os
import json
import re
from datetime import datetime, timezone
from typing import Dict

import boto3
from fastapi import FastAPI, HTTPException, Query

AWS_REGION = os.getenv("AWS_REGION", "us-east-1")
OUTPUT_BUCKET = os.getenv("OUTPUT_BUCKET")          # same bucket is fine
OUTPUT_PREFIX = os.getenv("OUTPUT_PREFIX", "mapreduce")

if not OUTPUT_BUCKET:
    raise RuntimeError("Missing env var OUTPUT_BUCKET")

s3 = boto3.client("s3", region_name=AWS_REGION)
app = FastAPI(title="Mapper", version="1.0")

WORD_RE = re.compile(r"[a-z0-9']+")

def parse_s3_url(s3_url: str):
    if not s3_url.startswith("s3://"):
        raise ValueError("chunk_url must start with s3://")
    rest = s3_url[len("s3://"):]
    bucket, key = rest.split("/", 1)
    return bucket, key

def count_words(text: str) -> Dict[str, int]:
    counts: Dict[str, int] = {}
    for w in WORD_RE.findall(text.lower()):
        counts[w] = counts.get(w, 0) + 1
    return counts

@app.get("/health")
def health():
    return {"ok": True}

@app.get("/map")
def map_chunk(
    chunk_url: str = Query(..., description="s3://bucket/key to the chunk"),
    job_id: str = Query(..., description="job id from splitter"),
    part: int = Query(..., ge=0, le=999, description="mapper part number"),
):
    try:
        in_bucket, in_key = parse_s3_url(chunk_url)
    except Exception as e:
        raise HTTPException(status_code=400, detail=str(e))

    # download chunk
    try:
        obj = s3.get_object(Bucket=in_bucket, Key=in_key)
        text = obj["Body"].read().decode("utf-8", errors="replace")
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to read chunk from S3: {e}")

    counts = count_words(text)

    # added metrics (ONLY addition)
    total_words = sum(counts.values())
    total_unique_words = len(counts)

    # upload result json
    out_key = f"{OUTPUT_PREFIX}/{job_id}/maps/part-{part:03d}.json"
    payload = {
        "job_id": job_id,
        "part": part,
        "input_chunk": chunk_url,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "total_words": total_words,
        "total_unique_words": total_unique_words,
        "counts": counts,
    }

    try:
        s3.put_object(
            Bucket=OUTPUT_BUCKET,
            Key=out_key,
            Body=json.dumps(payload).encode("utf-8"),
            ContentType="application/json",
        )
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to write map output to S3: {e}")

    return {
        "output_url": f"s3://{OUTPUT_BUCKET}/{out_key}",
        "total_words": total_words,
        "total_unique_words": total_unique_words,
    }
