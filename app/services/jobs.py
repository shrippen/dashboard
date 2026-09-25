"""Job registrations. Imported once by the scheduler."""

from app.services import analysis, audit, auth, data, hints, icons, notify
from app.services.scheduler import DAY, HOUR, MINUTE, every

ANALYSIS_EVERY = 5 * MINUTE


@every("housekeeping", HOUR)
def housekeeping() -> None:
    auth.purge()
    data.prune()
    hints.prune()
    audit.prune()


@every("icons", DAY)
def retry_icons() -> None:
    icons.forget_misses()


@every("analysis", ANALYSIS_EVERY)
def analyse() -> None:
    analysis.run_all()


@every("notify", MINUTE)
def notify_users() -> None:
    notify.dispatch()


@every("digest", 5 * MINUTE)
def digest_mails() -> None:
    notify.digests()
