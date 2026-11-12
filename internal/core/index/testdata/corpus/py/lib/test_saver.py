from .saver import save


def test_save():
    assert save(1) == 1
