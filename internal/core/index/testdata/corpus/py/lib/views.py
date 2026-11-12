from .app import route


@route("/index")
def index():
    return "index"


@route
def legacy():
    return "legacy"
