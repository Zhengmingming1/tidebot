package netproxy

import "context"

func lookupSystem(ctx context.Context) *System {
	out := run(ctx, "/usr/sbin/scutil", "--proxy")
	if out == "" {
		return nil
	}
	return ParseScutil(out)
}
