"""
pic-to-patch API server.

POST /patch          — upload image, returns {job_id}
GET  /jobs/{job_id}  — returns {status, result_url?}
GET  /jobs/{job_id}/result — returns the PNG directly

Jobs are queued via Redis (rq). Results are stored in Redis as blobs.
"""

import os
import uuid

from fastapi import FastAPI, UploadFile, HTTPException
from fastapi.responses import Response
import redis
import rq

REDIS_URL = os.environ.get("REDIS_URL", "redis://localhost:6379")
RESULT_TTL = int(os.environ.get("RESULT_TTL", "3600"))

app = FastAPI(title="pic-to-patch", version="0.2.0")
redis_conn = redis.from_url(REDIS_URL)
queue = rq.Queue("patches", connection=redis_conn)


@app.post("/patch")
async def create_patch(
    file: UploadFile,
    border_color: str = "#0a0a14",
    color_precision: int = 8,
    postprocess: bool = True,
):
    job_id = str(uuid.uuid4())
    input_bytes = await file.read()
    ext = (file.filename or "input.png").rsplit(".", 1)[-1] or "png"

    redis_conn.setex(f"job:{job_id}:input", RESULT_TTL, input_bytes)
    redis_conn.setex(f"job:{job_id}:ext", RESULT_TTL, ext)
    redis_conn.setex(f"job:{job_id}:status", RESULT_TTL, "processing")

    queue.enqueue(
        "worker.run_pipeline",
        job_id=job_id,
        border_color=border_color,
        color_precision=color_precision,
        postprocess=postprocess,
        job_timeout=300,
        result_ttl=RESULT_TTL,
    )

    return {"job_id": job_id}


@app.get("/jobs/{job_id}")
async def get_job(job_id: str):
    status = redis_conn.get(f"job:{job_id}:status")
    if status is None:
        raise HTTPException(404, "job not found")

    status = status.decode()
    resp = {"status": status}

    if status == "complete":
        resp["result_url"] = f"/jobs/{job_id}/result"
    elif status == "failed":
        error = redis_conn.get(f"job:{job_id}:error")
        resp["error"] = error.decode() if error else "unknown error"

    return resp


@app.get("/jobs/{job_id}/result")
async def get_result(job_id: str):
    data = redis_conn.get(f"job:{job_id}:result")
    if data is None:
        raise HTTPException(404, "result not ready")
    return Response(content=data, media_type="image/png",
                    headers={"Content-Disposition": f'inline; filename="{job_id}.png"'})


@app.get("/health")
async def health():
    try:
        redis_conn.ping()
    except Exception:
        raise HTTPException(503, "redis unavailable")
    return {"status": "ok"}
