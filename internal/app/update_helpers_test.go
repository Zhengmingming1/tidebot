package app

import "runtime"

func ServerAssetNameForTest() string { return ServerAssetName("9.9.9", runtime.GOOS, runtime.GOARCH) }

func runtimeHasArchive() bool { return runtime.GOOS != "windows" }
