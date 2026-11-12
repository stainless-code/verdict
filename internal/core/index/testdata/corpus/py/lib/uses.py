from .saver import save
from .store import Log


def flush(value, log: Log):
    log.save(value)
    return save(value)
