module example.invalid/stackgo

go 1.22.0

toolchain go1.22.3

require github.com/example/alpha v1.4.2

require (
	github.com/example/beta v0.9.0 // indirect
	"github.com/example/gamma" v2.0.1+incompatible
)
