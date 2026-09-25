"""Job registrations. Imported once by the scheduler."""

from app.services import audit, auth, data, hints, icons
from app.services.scheduler import DAY, HOUR, every


@every("housekeeping", HOUR)
def housekeeping() -> None:
    auth.purge()
    data.prune()
    hints.prune()
    audit.prune()


@every("icons", DAY)
def retry_icons() -> None:
    icons.forget_misses()
