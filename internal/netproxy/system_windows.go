package netproxy

import (
	"context"

	"golang.org/x/sys/windows/registry"
)

func lookupSystem(ctx context.Context) *System {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	en, _, _ := k.GetIntegerValue("ProxyEnable")
	srv, _, _ := k.GetStringValue("ProxyServer")
	ovr, _, _ := k.GetStringValue("ProxyOverride")
	pac, _, _ := k.GetStringValue("AutoConfigURL")
	return ParseWinINet(uint32(en), srv, ovr, pac)
}
