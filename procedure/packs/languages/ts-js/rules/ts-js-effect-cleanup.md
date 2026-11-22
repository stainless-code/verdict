Report a React effect that subscribes, schedules, or fetches without a teardown:
`useEffect` returning no cleanup for a listener, timer, or subscription, a
handler closed over a value its dependency list omits, or state written after
unmount.
