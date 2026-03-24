package main

import (
	"github.com/todd-chamberlain/nstack/pkg/engine"
	"github.com/todd-chamberlain/nstack/pkg/stages/s1_provision"
	"github.com/todd-chamberlain/nstack/pkg/stages/s3_networking"
	"github.com/todd-chamberlain/nstack/pkg/stages/s4_gpu"
	"github.com/todd-chamberlain/nstack/pkg/stages/s5_slurm"
	"github.com/todd-chamberlain/nstack/pkg/stages/s6_mlops"
)

func buildRegistry() *engine.Registry {
	r := engine.NewRegistry()
	r.Register(s1_provision.New())
	r.Register(s3_networking.New())
	r.Register(s4_gpu.New())
	r.Register(s5_slurm.New())
	r.Register(s6_mlops.New())
	return r
}
