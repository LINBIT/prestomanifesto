package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	log "github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
	"golang.org/x/sync/errgroup"

	"github.com/LINBIT/prestomanifesto/pkg/merge"
)

func main() {
	archs := flag.String("a", "amd64,s390x,ppc64le,arm64", "',' separated list of architecture prefixes to process")
	allArchs := flag.String("all", "amd64,s390x,ppc64le,arm64", "',' separated list of all architecture prefixes")
	loopDuration := flag.Duration("d", 0, "if set to something not '0', execute in a loop every given time.Duration")
	dryRun := flag.Bool("dry-run", false, "only print what would happen")
	logLevel := flag.String("loglevel", "info", "log level as defined in logrus")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage of %s [opts] registrydomain:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), "docker credentials are used from ~/.docker/config.json\n")
	}

	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	lvl, err := log.ParseLevel(*logLevel)
	if err != nil {
		log.Fatalf("'%s' is not a valid logrus log level\n", *logLevel)
	}
	log.SetLevel(lvl)

	allArchsSplit, archsSplit := strings.Split(*allArchs, ","), strings.Split(*archs, ",")
	if len(allArchsSplit) == 0 {
		log.Fatal("list of '-all' architectures not allowed to be empty")
	}
	if len(archsSplit) == 0 {
		log.Fatal("list of '-a' architectures not allowed to be empty")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	domain := flag.Arg(0)
	reg, err := name.NewRegistry(domain)
	if err != nil {
		log.Fatal(fmt.Errorf("name.NewRegistry(%s): %w", domain, err))
	}

	for {
		err = run(ctx, reg, allArchsSplit, archsSplit, *dryRun)
		if err != nil {
			log.Fatal(fmt.Errorf("run(ctx, reg, %v, %v, %t): %w", allArchsSplit, archsSplit, *dryRun, err))
		}
		if *loopDuration == 0 {
			break
		}
		log.Debugf("sleeping %s\n", *loopDuration)
		time.Sleep(*loopDuration)
	}
}

func run(ctx context.Context, reg name.Registry, allArchs, archs []string, dryRun bool) error {
	puller, err := remote.NewPuller(remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithTransport(DebugLogRoundTripper{}))
	if err != nil {
		return fmt.Errorf("remote.NewPuller(): %w", err)
	}

	pusher, err := remote.NewPusher(remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithTransport(DebugLogRoundTripper{}))
	if err != nil {
		return fmt.Errorf("remote.NewPusher(): %w", err)
	}

	repoInfo, err := getRepoInfo(reg, allArchs, archs, remote.Reuse(puller), remote.Reuse(pusher))
	if err != nil {
		return fmt.Errorf("getRepoInfo(ctx, %s): %w", reg, err)
	}

	for dest, src := range repoInfo {
		var args strings.Builder
		for i := range src.img {
			digest, _ := src.img[i].Digest()
			fmt.Fprintf(&args, "--manifest %s@%s ", src.tag[i], digest)
		}

		fmt.Printf("crane index append --docker-empty-base %s--tag %s\n", args.String(), dest)
		if !dryRun {
			err = merge.Merge(src.img, dest, remote.Reuse(puller), remote.Reuse(pusher))
			if err != nil {
				return fmt.Errorf("Merge(%s, %s): %w", src, dest, err)
			}
		}
	}

	return nil
}

func getRepoInfo(registry name.Registry, allArchs, validArch []string, opts ...remote.Option) (map[name.Tag]SourceImages, error) {
	repos, err := remote.Catalog(context.Background(), registry, opts...)
	if err != nil {
		return nil, fmt.Errorf("remote.Catalog(ctx, \"%s\"): %w", registry, err)
	}

	log.Debugf("remote.Catalog(ctx, \"%s\") = %s", registry, strings.Join(repos, " "))

	repoByBase := make(map[name.Repository][]name.Repository)
	for _, repo := range repos {
		parts := strings.SplitN(repo, "/", 2)
		arch, ok := processArch(parts[0], allArchs, validArch)
		if ok && arch != "" {
			repoByBase[registry.Repo(parts[1])] = append(repoByBase[registry.Repo(parts[1])], registry.Repo(repo))
		}
	}

	destImages := make(map[name.Tag]SourceImages)
	lock := sync.Mutex{}
	g := errgroup.Group{}
	for dest, archRepos := range repoByBase {
		dest := dest
		archRepos := archRepos

		g.Go(func() error {
			log.Debugf("Processing %s = %v", dest, archRepos)

			for _, archRepo := range archRepos {
				arch := strings.SplitN(archRepo.RepositoryStr(), "/", 2)[0]

				tags, err := remote.List(archRepo, opts...)
				if err != nil {
					return fmt.Errorf("remote.List(%s): %w", archRepo, err)
				}

				log.Debugf("remote.List(%s) = %s", archRepo, strings.Join(tags, " "))

				for _, tag := range tags {
					archRepoTag := archRepo.Tag(tag)
					opts := slices.Clone(opts)
					img, err := remote.Image(archRepoTag, append(opts, remote.WithPlatform(v1.Platform{OS: "linux", Architecture: arch}))...)
					if err != nil {
						return fmt.Errorf("remote.Image(%s): %w", archRepoTag, err)
					}

					lock.Lock()
					d := destImages[dest.Tag(tag)]
					d.Append(img, archRepoTag)
					destImages[dest.Tag(tag)] = d
					lock.Unlock()
				}
			}

			return nil
		})
	}

	err = g.Wait()
	if err != nil {
		return nil, err
	}

	return destImages, nil
}

// Returns the validated arch prefix, and true if it is valid arch to process.
func processArch(prefix string, allArchs, validArch []string) (string, bool) {
	for _, arch := range allArchs {
		if prefix == arch { // it is an arch, but not sure if we want to process
			for _, valArch := range validArch {
				if prefix == valArch {
					return prefix, true
				}
			}
			// arch, but not interested
			return prefix, false
		}
	}

	// not an arch, so it is a toplevel repo
	return "", true
}

// SourceImages wraps a list of images to make debug prints more readable
type SourceImages struct {
	img []v1.Image
	tag []name.Tag
}

func (s *SourceImages) Append(img v1.Image, tag name.Tag) {
	s.img = append(s.img, img)
	s.tag = append(s.tag, tag)
}

// DebugLogRoundTripper is a http.RoundTripper that logs the method, URL and result of every request on debug level.
type DebugLogRoundTripper struct{}

func (d DebugLogRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		log.Debugf("%s: %s = %s", req.Method, req.URL, err)
	} else {
		log.Debugf("%s: %s = %d", req.Method, req.URL, resp.StatusCode)
	}

	return resp, err
}
