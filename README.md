# prestomanifesto

This is an opinionated tool to generate multi-arch docker registries.

# Assumptions
- a proper docker registry served via `https`
- an account that is able to do administrative tasks, that has its credentials in `~/.docker/config.json`
  (i.e., `docker login $domain`) was successfully executed.
- images are stored in architecture specific namespaces (i.e., `$domain/$arch/$image:$tag`, where `$arch`
  is arbitrary, but usually something like `amd64`, `ppc64le`,...)

# What does it do?
`prestomanifesto` (concurrently) crawls a registry and collects all architecture specific repositories and tags.
It then generates the top-level multi-arch image manifests and pushes them. If the image manifests are already
up-to-date, no actual change will be made.

# Example
```
$ prestomanifesto registry.io
crane index append --docker-empty-base --manifest registry.io/amd64/rck:latest --manifest registry.io/s390x/rck:latest --tag registry.io/rck:latest
```

# Docker
This requires a bind mount for `config.json`, containing the credentials for the registry:

```
docker run -it --rm \
  -v ~/.docker/config.json:/etc/docker/config.json:ro linbit/prestomanifesto -dry-run registry.io
```
