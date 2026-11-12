package run

import "strings"

func displayPassThrough(args, environ []string) ([]string, bool) {
	shape := make([]string, len(args))
	for i := range shape {
		shape[i] = redactedValue
	}
	safe := true
	known := secretValues(environ)
	for i := 0; i < len(args); {
		n, ok := displayOption(args[i:], known)
		if ok {
			copy(shape[i:i+n], args[i:i+n])
		} else {
			safe = false
		}
		i += n
	}
	return shape, safe
}

func displayOption(args, known []string) (int, bool) {
	name, value, inline := strings.Cut(args[0], "=")
	switch name {
	case "--model", "-m", "--max-budget-usd", "--max-cost", "--max-turns":
	default:
		return 1, false
	}
	if inline {
		return 1, displayValue(value, known)
	}
	if len(args) < 2 {
		return 1, false
	}
	return 2, displayValue(args[1], known)
}

func displayValue(value string, known []string) bool {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.:/"
	return value != "" && strings.Trim(value, chars) == "" && !isSecretValue(value, known)
}

func printablePassThrough(args, environ []string) bool {
	_, safe := displayPassThrough(args, environ)
	return safe
}

func (st *state) displayArgv(args *launchArgs) ([]string, []string) {
	shape, secrets := recordArgv(args.argv, args.prompt, st.req.Environ)
	displayed, _ := displayPassThrough(st.req.PassThrough, st.req.Environ)
	offset := 1 + len(args.def.Args)
	for i, value := range displayed {
		if value == redactedValue {
			secrets = append(secrets, st.req.PassThrough[i])
		}
		shape[offset+i] = value
	}
	return shape, secrets
}
