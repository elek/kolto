package main

import (
	"context"
	"encoding/json"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/apicaps"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"os"
	"slices"
	"strings"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "llb" {
		def, err := getLLB()
		if err != nil {
			panic(err)
		}
		err = llb.WriteTo(def, os.Stdout)
		if err != nil {
			panic(err)
		}
		return
	}
	if err := grpcclient.RunFromEnvironment(appcontext.Context(), Build); err != nil {
		logrus.Errorf("fatal error: %+v", err)
		panic(err)
	}
}

func Build(ctx context.Context, cl client.Client) (*client.Result, error) {
	lines, err := ReadPoem(context.Background(), cl)
	if err != nil {
		if err != nil {
			return nil, errors.WithStack(err)
		}
	}

	def, err := getLLB()
	if err != nil {
		return nil, err
	}
	res, err := cl.Solve(ctx, client.SolveRequest{
		Definition: def.ToPB(),
	})

	if err != nil {
		return nil, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}
	res.SetRef(ref)

	var image specs.Image

	imageConfig := res.Metadata["containerimage.config"]

	_ = json.Unmarshal(imageConfig, &image)

	image.History = []specs.History{}

	slices.Reverse(lines)
	for ix, l := range lines {
		t := time.Now().Add(time.Duration(-ix) * time.Second)
		image.History = append(image.History, specs.History{
			CreatedBy:  l,
			EmptyLayer: true,
			Created:    &t,
		})
	}

	bytes, err := json.Marshal(image)
	if bytes != nil {
		if err != nil {
			return nil, errors.WithStack(err)
		}
	}

	res.AddMeta("containerimage.config", bytes)
	return res, nil

}

func getLLB() (*llb.Definition, error) {
	def := &llb.Definition{}
	def.Metadata = make(map[digest.Digest]pb.OpMetadata)

	addLayer(
		def,
		source("docker-image://docker.io/library/alpine:latest@sha256:c5b1261d6d3e43071626931fc004f70149baeba2c8ec672bd4f27761f8e1ad6b"),
		cap("source.image"),
	)

	addLayer(def, inputFromPrev(def))

	return def, nil
}

type opOpt func(op *pb.Op, md *pb.OpMetadata)

func inputFromPrev(def *llb.Definition) opOpt {
	return func(op *pb.Op, md *pb.OpMetadata) {
		op.Inputs = append(op.Inputs, &pb.Input{
			Digest: digest.FromBytes(def.Def[len(def.Def)-1]),
			Index:  0,
		})
	}
}

func exec(cmd string) opOpt {
	return func(op *pb.Op, md *pb.OpMetadata) {
		op.Op = &pb.Op_Exec{
			Exec: &pb.ExecOp{
				Meta: &pb.Meta{
					Args: []string{"bin/sh", "-c", cmd},
					Cwd:  "/",
				},
				Mounts: []*pb.Mount{
					{
						Input:  0,
						Dest:   "/",
						Output: 0,
					},
				},
			},
		}
	}
}

func desc(desc string) opOpt {
	return func(op *pb.Op, md *pb.OpMetadata) {
		md.Description = map[string]string{
			"com.docker.dockerfile.v1.command": desc,
			"llb.customname":                   desc,
		}
	}
}

func cap(name apicaps.CapID) opOpt {
	return func(op *pb.Op, md *pb.OpMetadata) {
		if md.Caps == nil {
			md.Caps = make(map[apicaps.CapID]bool)
		}
		md.Caps[name] = true
	}
}

func source(name string) opOpt {
	return func(op *pb.Op, md *pb.OpMetadata) {
		op.Op = &pb.Op_Source{
			Source: &pb.SourceOp{
				Identifier: name,
			},
		}
	}
}

func addLayer(def *llb.Definition, opts ...opOpt) {
	proto := &pb.Op{}
	metadata := &pb.OpMetadata{}
	for _, do := range opts {
		do(proto, metadata)
	}

	dt, err := proto.Marshal()
	if err != nil {
		panic(err)
	}
	def.Def = append(def.Def, dt)
	dgst := digest.FromBytes(dt)

	def.Metadata[dgst] = *metadata

}

func ReadPoem(ctx context.Context, c client.Client) ([]string, error) {
	opts := c.BuildOpts().Opts

	filename := opts["file"]
	if filename == "" {
		filename = "Poem"
	}

	src := llb.Local("dockerfile",
		llb.IncludePatterns([]string{filename}),
		llb.SessionID(c.BuildOpts().SessionID),
		llb.SharedKeyHint("Poem"),
		llb.WithCustomName("[internal] reading the Poem"),
	)

	def, err := src.Marshal(ctx)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	res, err := c.Solve(ctx, client.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	var content []byte
	content, err = ref.ReadFile(ctx, client.ReadRequest{
		Filename: filename,
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	lines := strings.Split(string(content), "\n")
	return lines[1:], nil
}
