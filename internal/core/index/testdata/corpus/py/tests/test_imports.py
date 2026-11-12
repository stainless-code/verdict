import lib


def test_module():
    assert lib.saver.save(3) == 3
