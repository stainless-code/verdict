Report a mutable default argument (`=[]`, `={}`, `=set()`) that the function
mutates or returns: the default is created once and shared across every call, so
state leaks between requests.
