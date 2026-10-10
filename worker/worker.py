import hashlib
import logging
import os
import signal
import time

import requests

API_URL = os.getenv("JOBQUEUE_URL", "http://localhost:8080")
POLL_INTERVAL = float(os.getenv("POLL_INTERVAL", "1.0"))
REQUEST_TIMEOUT = 10

log = logging.getLogger("worker")
running = True


def handle_stop(signum, frame):
    global running
    log.info("stop signal received, finishing current job...")
    running = False


# ---- job handlers: each takes the payload dict and returns a result string ----

def word_count(payload):
    text = payload.get("text")
    if not isinstance(text, str):
        raise ValueError("payload.text (string) is required")
    return str(len(text.split()))


def hash_file(payload):
    path = payload.get("path")
    if not path:
        raise ValueError("payload.path is required")
    digest = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            digest.update(chunk)
    return digest.hexdigest()


def fetch_url(payload):
    url = payload.get("url")
    if not url:
        raise ValueError("payload.url is required")
    resp = requests.get(url, timeout=REQUEST_TIMEOUT)
    return str(resp.status_code)


HANDLERS = {
    "word_count": word_count,
    "hash_file": hash_file,
    "fetch_url": fetch_url,
}


# ---- talking to the API ----

def claim_job():
    """Ask the API for the next job. Returns a dict, or None if the queue is empty."""
    resp = requests.post(f"{API_URL}/jobs/claim", timeout=REQUEST_TIMEOUT)
    if resp.status_code == 204:
        return None
    resp.raise_for_status()
    return resp.json()


def report_complete(job_id, result):
    resp = requests.post(
        f"{API_URL}/jobs/{job_id}/complete",
        json={"result": result},
        timeout=REQUEST_TIMEOUT,
    )
    resp.raise_for_status()


def report_fail(job_id, error):
    resp = requests.post(
        f"{API_URL}/jobs/{job_id}/fail",
        json={"error": error},
        timeout=REQUEST_TIMEOUT,
    )
    resp.raise_for_status()


def process(job):
    job_id = job["id"]
    job_type = job["type"]
    handler = HANDLERS.get(job_type)
    if handler is None:
        report_fail(job_id, f"unknown job type: {job_type}")
        return

    try:
        result = handler(job.get("payload") or {})
    except Exception as exc:  # any handler error becomes a failed job
        log.warning("job %s failed: %s", job_id, exc)
        report_fail(job_id, str(exc))
        return

    report_complete(job_id, result)
    log.info("job %s (%s) succeeded: %s", job_id, job_type, result)


def main():
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(message)s",
    )
    signal.signal(signal.SIGINT, handle_stop)
    signal.signal(signal.SIGTERM, handle_stop)

    log.info("worker started, polling %s", API_URL)
    while running:
        try:
            job = claim_job()
        except requests.RequestException as exc:
            log.error("cannot reach API: %s", exc)
            time.sleep(POLL_INTERVAL * 3)
            continue

        if job is None:
            time.sleep(POLL_INTERVAL)
            continue

        log.info("claimed job %s (%s, attempt %s)", job["id"], job["type"], job["attempts"])
        try:
            process(job)
        except requests.RequestException as exc:
            log.error("could not report result for job %s: %s", job["id"], exc)

    log.info("worker stopped")


if __name__ == "__main__":
    main()