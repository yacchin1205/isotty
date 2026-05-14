package isotty

import "runtime/debug"

var defaultVersion = "dev"

func Version() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return defaultVersion
}
