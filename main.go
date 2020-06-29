package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/genuinetools/reg/registry"
	"github.com/genuinetools/reg/repoutils"
	digest "github.com/opencontainers/go-digest"
	"golang.org/x/sync/errgroup"
)

func main() {
	archs := flag.String("a", "amd64,s390x", "',' separated list of architecture prefixes to process")
	allArchs := flag.String("all", "amd64,s390x,ppc64le,arm64", "',' separated list of all architecture prefixes")
	loopDuration := flag.Duration("d", 0, "if set to something not '0', execute in a loop every given time.Duration")
	dryRun := flag.Bool("dry-run", false, "print 'docker manifest' commands on stdout, but don't execute them")
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
	allArchsSplit, archsSplit := strings.Split(*allArchs, ","), strings.Split(*archs, ",")
	if len(allArchsSplit) == 0 {
		log.Fatal("list of '-all' architectures not allowed to be empty")
	}
	if len(archsSplit) == 0 {
		log.Fatal("list of '-a' architectures not allowed to be empty")
	}

	domain := flag.Arg(0)

	ctx := context.Background()

	reg, err := getRegistry(ctx, "", "", domain)
	if err != nil {
		log.Fatal(err)
	}

	for {
		err = run(ctx, reg, allArchsSplit, archsSplit, *dryRun)
		if err != nil {
			log.Fatal(err)
		}
		if *loopDuration == 0 {
			break
		}
		log.Printf("sleeping %s\n", *loopDuration)
		time.Sleep(*loopDuration)
	}
}

func run(ctx context.Context, reg *registry.Registry, allArchs, archs []string, dryRun bool) error {
	repoTags, err := getAllRepoTags(ctx, reg)
	if err != nil {
		return err
	}

	updates, err := getUpdates(ctx, reg, repoTags, allArchs, archs)
	if err != nil {
		return err
	}

	nrUpdates := len(updates)
	log.Printf("number of updates: %d\n", nrUpdates)
	if nrUpdates == 0 {
		return nil
	}

	if err := pushUpdates(updates, reg.Domain, dryRun); err != nil {
		return err
	}

	return nil
}

type updateInfo struct {
	repoTag string
	archs   []string
}

func pushUpdates(updateInfo []updateInfo, domain string, dryRun bool) error {
	createCmdArgs := []string{"manifest", "create", "--insecure", "--amend"}
	pushCmdArgs := []string{"manifest", "push", "--insecure"}

	if err := rmDockerManifests(dryRun); err != nil {
		return err
	}
	for _, u := range updateInfo {
		topLevel := fmt.Sprintf("%s/%s", domain, u.repoTag)
		cCmdArgs := append(createCmdArgs, topLevel)
		for _, a := range u.archs {
			cCmdArgs = append(cCmdArgs, fmt.Sprintf("%s/%s/%s", domain, a, u.repoTag))
		}

		if err := execPrint("docker", cCmdArgs, dryRun); err != nil {
			return err
		}

		pCmdArgs := append(pushCmdArgs, topLevel)
		if err := execPrint("docker", pCmdArgs, dryRun); err != nil {
			return err
		}
	}

	return nil
}

func execPrint(name string, args []string, dryRun bool) error {
	fmt.Println(name, strings.Join(args, " "))

	if dryRun {
		return nil
	}

	log.Println("executing command")
	cmd := exec.Command(name, args...)
	stdoutStderr, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s\n", stdoutStderr)

	return nil
}

func getUpdates(ctx context.Context, reg *registry.Registry, repoTags map[string][]string, allArchs, validArch []string) ([]updateInfo, error) {
	type containerInfo struct {
		digs  map[digest.Digest]int
		archs []string
	}

	var (
		m       sync.Mutex
		g       errgroup.Group
		digests = make(map[string]containerInfo)
	)

	for repo, tags := range repoTags {
		rSplit := strings.Split(repo, "/")
		arch, ok := processArch(rSplit[0], allArchs, validArch)
		if !ok {
			continue
		}
		isArch := (arch != "")

		for _, tag := range tags {
			log.Printf("%s:%s\n", repo, tag)
			tag := tag
			g.Go(func() error {
				if isArch { // registry.com/arm64/image:tag
					image, err := registry.ParseImage(fmt.Sprintf("%s/%s:%s", reg.Domain, repo, tag))
					if err != nil {
						return err
					}
					log.Printf("\tget digest for %s\n", image)
					dig, err := reg.Digest(ctx, image)
					if err != nil {
						return err
					}
					repoNoarch := strings.Join(rSplit[1:], "/")
					repoTag := repoNoarch + ":" + tag
					log.Printf("\tdigests[%s][%s] += 1", repoTag, dig)
					m.Lock()
					ee, ok := digests[repoTag]
					if !ok {
						ee = containerInfo{
							digs: make(map[digest.Digest]int),
						}
					}
					ee.archs = append(ee.archs, arch)
					ee.digs[dig]++
					digests[repoTag] = ee
					m.Unlock()
				} else { // "toplevel"; registry.com/image:tag
					ml, err := reg.ManifestList(ctx, repo, tag)
					if err != nil {
						return err
					}
					repoTag := repo + ":" + tag
					m.Lock()
					if digests[repoTag].digs == nil {
						info := containerInfo{
							digs: make(map[digest.Digest]int),
						}
						digests[repoTag] = info
					}
					for _, m := range ml.Manifests {
						dig := m.Digest
						log.Printf("\tdigests[%s][%s] -= 1", repoTag, dig)
						digests[repoTag].digs[dig]--
					}
					m.Unlock()
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
	}

	var updates []updateInfo
	for rt, info := range digests {
		var needsUpdate bool
		for _, n := range info.digs {
			if n != 0 {
				needsUpdate = true
				break
			}
		}
		if needsUpdate {
			updates = append(updates, updateInfo{repoTag: rt, archs: info.archs})
		}
	}

	return updates, nil
}

func getAllRepoTags(ctx context.Context, reg *registry.Registry) (map[string][]string, error) {
	repos, err := reg.Catalog(ctx, "")
	if err != nil {
		if _, ok := err.(*json.SyntaxError); ok {
			return nil, fmt.Errorf("domain %s is not a valid registry", reg.Domain)
		}
		return nil, err
	}
	var (
		m        sync.Mutex
		g        errgroup.Group
		repoTags = map[string][]string{}
	)

	for _, repo := range repos {
		repo := repo
		g.Go(func() error {
			tags, err := reg.Tags(ctx, repo)
			if err != nil {
				fmt.Printf("get tags of [%s] error: %s", repo, err)
				return err
			}
			m.Lock()
			repoTags[repo] = tags
			m.Unlock()

			return nil
		})
	}

	return repoTags, g.Wait()
}

func getRegistry(ctx context.Context, username, password, domain string) (*registry.Registry, error) {
	auth, err := repoutils.GetAuthConfig(username, password, domain)
	if err != nil {
		return nil, err
	}

	reg, err := registry.New(ctx, auth, registry.Opt{Domain: "https://" + domain})
	if err != nil {
		return nil, err
	}

	return reg, nil
}

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

// even with --amend I had very weird results
// the .docker/manifests/ are basicall a scratch space anyways
// and usually this runs in a container, so don't be too picky about rm -rf'ing it
func rmDockerManifests(dryRun bool) error {
	manifestDir := []string{"~", ".docker", "manifests"}
	if dryRun {
		fmt.Println("rm -rf ", path.Join(manifestDir...))
		return nil
	}

	cUser, err := user.Current()
	if err != nil {
		return err
	}

	manifestDir[0] = cUser.HomeDir
	return os.RemoveAll(path.Join(manifestDir...))
}
