# Docker Graph Utility

Hack the docker graph directory

## Scramble

Scrambles image IDs based on change to separate image and graph driver IDs, based on https://github.com/docker/docker/pull/16491


## Downgrade

Clean up the graph directory for compatibility with older Docker engines 


## TODO

Currently container references are not updated. Ensure all containers are stopped and removed before running
