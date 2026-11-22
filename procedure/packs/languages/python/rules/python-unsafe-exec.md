Report untrusted input reaching an interpreter: `eval`, `exec`, `pickle.loads`,
`yaml.load` without `SafeLoader`, `os.system`, `subprocess` with `shell=True`,
or a template rendered from request data.
