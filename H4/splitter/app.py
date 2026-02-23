# app.py
import os
import uuid
from datetime import datetime, timezone
from typing import List

import boto3
import requests
from fastapi import FastAPI, HTTPException, Query

AWS_REGION = os.getenv("AWS_REGION", "us-east-1")
OUTPUT_BUCKET = os.getenv("OUTPUT_BUCKET")  # required
OUTPUT_PREFIX = os.getenv("OUTPUT_PREFIX", "jobs")  

if not OUTPUT_BUCKET:
    raise RuntimeError("Missing env var OUTPUT_BUCKET")

# Create S3 client
s3 = boto3.client("s3", region_name=AWS_REGION)   
app = FastAPI(title="Splitter", version="1.0")


def _job_id() -> str:
    ts = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    return f"{ts}_{uuid.uuid4().hex[:8]}"


def _split_lines(lines: List[str], n: int) -> List[str]:
    """
    Split list of lines into n roughly-equal chunks.
    Guarantees n chunks (some may be empty if n > #lines).
    """
    total = len(lines)
    base = total // n
    rem = total % n
    chunks = []
    start = 0
    for i in range(n):
        extra = 1 if i < rem else 0
        end = start + base + extra
        chunk_lines = lines[start:end]
        chunks.append("".join(chunk_lines))
        start = end
    return chunks


@app.get("/health")
def health():
    return {"ok": True}


@app.get("/split")
def split(
    input_url: str = Query(..., description="HTTP/HTTPS URL of the text file"),
    n: int = Query(3, ge=1, le=200, description="Number of chunks to create"),
):
    # 1) download
    try:
        r = requests.get(input_url, timeout=30)
        r.raise_for_status()
        text = r.text
    except Exception as e:
        raise HTTPException(status_code=400, detail=f"Failed to download input_url: {e}")

    lines = text.splitlines(keepends=True)
    chunks = _split_lines(lines, n)

    # unique identifier for one split request.
    job_id = _job_id()
    chunk_urls = []

    for i, chunk_text in enumerate(chunks):
        key = f"{OUTPUT_PREFIX}/{job_id}/chunks/chunk-{i:03d}.txt"
        try:
            s3.put_object(
                Bucket=OUTPUT_BUCKET,
                Key=key,
                Body=chunk_text.encode("utf-8"),
                ContentType="text/plain; charset=utf-8",
            )
        except Exception as e:
            raise HTTPException(status_code=500, detail=f"Failed to upload chunk {i}: {e}")

        chunk_urls.append(f"s3://{OUTPUT_BUCKET}/{key}")

    return {
        "job_id": job_id,
        "num_chunks": n,
        "chunk_urls": chunk_urls,
        "input_url": input_url,
    }
