Report blocking work inside an `async def`: a synchronous HTTP, file, or
database call, `time.sleep`, or a long CPU loop that holds the event loop for
every other task. The fix is the async client, an executor, or a thread — never
a wider timeout.
