"""
pic-to-patch worker. Picks jobs from Redis queue and runs the pipeline.

Run with: rq worker patches --url redis://localhost:6379
"""

import os
import tempfile
from pathlib import Path

import redis

from pipeline.convert import convert, convert_svg

REDIS_URL = os.environ.get("REDIS_URL", "redis://localhost:6379")
RESULT_TTL = int(os.environ.get("RESULT_TTL", "3600"))
redis_conn = redis.from_url(REDIS_URL)


def run_pipeline(job_id, border_color="#0a0a14",
                 color_precision=8, postprocess=True):
    try:
        input_bytes = redis_conn.get(f"job:{job_id}:input")
        ext = (redis_conn.get(f"job:{job_id}:ext") or b"png").decode()
        if not input_bytes:
            raise RuntimeError("input not found in redis")

        is_svg = ext.lower() == "svg"

        with tempfile.TemporaryDirectory(prefix="p2p_") as tmpdir:
            tmpdir = Path(tmpdir)
            input_path = tmpdir / f"input.{ext}"
            input_path.write_bytes(input_bytes)
            output_path = tmpdir / "patch.png"

            if is_svg:
                convert_svg(str(input_path), str(output_path),
                            border_color=border_color, postprocess=postprocess)
            else:
                convert(str(input_path), str(output_path),
                        border_color=border_color, color_precision=color_precision,
                        postprocess=postprocess)

            result_bytes = output_path.read_bytes()

        redis_conn.setex(f"job:{job_id}:result", RESULT_TTL, result_bytes)
        redis_conn.setex(f"job:{job_id}:status", RESULT_TTL, "complete")
        redis_conn.delete(f"job:{job_id}:input")

    except Exception as e:
        redis_conn.setex(f"job:{job_id}:status", RESULT_TTL, "failed")
        redis_conn.setex(f"job:{job_id}:error", RESULT_TTL, str(e))
        raise
