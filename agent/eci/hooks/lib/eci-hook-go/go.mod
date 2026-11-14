module github.com/xaionaro-go/eci-hook

go 1.24

require github.com/xaionaro-go/eci-command-plan v0.0.0

require mvdan.cc/sh/v3 v3.12.0 // indirect

// TODO: this local filesystem replace violates coding-go Modules policy; move it to a go.work with a path relative to that go.work.
replace github.com/xaionaro-go/eci-command-plan => ../eci-command-plan-go
