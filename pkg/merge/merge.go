package merge

import (
	"sort"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// Merge merges the source images into a single manifest list.
func Merge(src []v1.Image, dest name.Tag, opts ...remote.Option) error {
	var mutations []mutate.IndexAddendum
	for _, img := range src {
		digest, err := img.Digest()
		if err != nil {
			return err
		}

		mediaType, err := img.MediaType()
		if err != nil {
			return err
		}

		cfg, err := img.ConfigFile()
		if err != nil {
			return err
		}

		mutations = append(mutations, mutate.IndexAddendum{
			Descriptor: v1.Descriptor{
				MediaType: mediaType,
				Platform:  cfg.Platform(),
				Digest:    digest,
			},
			Add: img,
		})
	}

	// Ensure consistent platform order
	sort.Slice(mutations, func(i, j int) bool {
		return mutations[i].Platform.String() < mutations[j].Platform.String()
	})

	index := mutate.AppendManifests(empty.Index, mutations...)
	index = mutate.IndexMediaType(index, types.DockerManifestList)

	return remote.WriteIndex(dest, index, opts...)
}
