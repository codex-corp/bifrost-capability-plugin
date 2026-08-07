package main

import "github.com/maximhq/bifrost/core/schemas"

func GetName() string { return "agent-capability-router-abi-probe" }

func Init(any) error { return nil }

func Cleanup() error { return nil }

func PreRequestHook(*schemas.BifrostContext, *schemas.BifrostRequest) error { return nil }
