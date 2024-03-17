docker buildx build . -t ghcr.io/elek/kolto
docker buildx build . -f Poem -t test2 --no-cache
docker history test2
