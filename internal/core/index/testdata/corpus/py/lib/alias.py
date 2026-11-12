from .saver import save as persist


def store(value):
    return persist(value)
