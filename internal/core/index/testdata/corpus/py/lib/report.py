from .saver import save


def describe(value):
    return f"saved {save(value)}"
