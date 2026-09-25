"""Background jobs (one process, one scheduler).

    every 5 min   rules → hints          (analysis)
    every 1 min   notifications, digests
    hourly        housekeeping (sessions, cache, hints, audit)
    daily         retry missing icons
"""

import logging
from collections.abc import Callable
from dataclasses import dataclass

from apscheduler.schedulers.background import BackgroundScheduler

log = logging.getLogger(__name__)

MINUTE = 60
HOUR = 60 * MINUTE
DAY = 24 * HOUR


@dataclass(frozen=True)
class Job:
    name: str
    seconds: int
    run: Callable[[], None]


_jobs: list[Job] = []
_scheduler: BackgroundScheduler | None = None


def every(name: str, seconds: int):
    """Decorator: register a job. Services register their own jobs."""

    def wrap(func: Callable[[], None]) -> Callable[[], None]:
        _jobs.append(Job(name, seconds, func))
        return func

    return wrap


def _safe(job: Job) -> Callable[[], None]:
    def run() -> None:
        try:
            job.run()
        except Exception:  # One failing job must not stop the others.
            log.exception("job %s failed", job.name)

    return run


def start() -> None:
    global _scheduler
    from app.services import jobs  # noqa: F401  (registers all jobs)

    _scheduler = BackgroundScheduler(timezone="UTC")
    for job in _jobs:
        _scheduler.add_job(_safe(job), "interval", seconds=job.seconds, id=job.name,
                           max_instances=1, coalesce=True)
    _scheduler.start()
    log.info("scheduler started: %s", ", ".join(j.name for j in _jobs))


def stop() -> None:
    if _scheduler is not None and _scheduler.running:
        _scheduler.shutdown(wait=False)


def run_now(name: str) -> None:
    for job in _jobs:
        if job.name == name:
            job.run()
