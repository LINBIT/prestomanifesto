# prestomanifesto

This is an opinionated tool to generate multi-arch docker registries.

# Assumptions
- a proper docker registry served via `https`
- an account that is able to do administrative tasks, that has its credentials in `~/.docker/config.json`
  (i.e., `docker login $domain`) was successfully executed.
- images are stored in architecture specific namespaces (i.e., `$domain/$arch/$image:$tag`, where `$arch`
  is arbitrary, but usually something like `amd64`, `ppc64le`,...)

# What does it do?
`prestomanifesto` (concurrently) crawls a registry and checks if the top-level manifest list (i.e.,
`$domain/$image:$tag`) consists of all matching architecture images/tags (i.e., `$domain/$arch/$image:$tag`
for all `$arch`). It does that for all repositories.

If a top-level manifest needs update, the commands to update it are printed on `stdout`.
The brave can therefore execute it like:

```
prestomanifesto $domain 2>/dev/null | sh
```

# Example
```
$ prestomanifesto registry.io
docker manifest create --insecure --amend registry.io/rck:latest registry.io/s390x/rck:latest registry.io/amd64/rck:latest
docker manifest push --insecure registry.io/rck:latest
```

# Docker
This requires two bind mounts:
- the docker socket
- a `docker.json` that contains the credentials for registry

```
docker run -it --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v ~/.docker/config.json:/etc/docker/config.json linbit/prestomanifesto registry.io
```

If the image is started with `--force` the output is actually executed. Be careful.

```
docker run -it --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v ~/.docker/config.json:/etc/docker/config.json linbit/prestomanifesto --force registry.io
```
