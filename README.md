# Mirror
Packagist mirror

## Install

```shel
go get -u github.com/wudi/mirror
````

## Usage

```shell
mirror -h                                                                                                                                         [5f82231]
NAME:
   mirror - a packagist mirror tool

USAGE:
   mirror [global options] command [command options] [arguments...]

DESCRIPTION:
   For create packagist mirror

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   -c value                Number of multiple requests to make at a time (default: 30)
   -a value                Number of attempt times (default: 5)
   -i value                Interval (default: 60)
   --mirror value          Mirror url (default: "https://packagist.org")
   --proxy value           Set proxy for request. eg: http://ip:port
   --timeout value         timeout (default: 30s)
   --redis-host value      Redis server hostname (default: "127.0.0.1")
   --redis-port value      Redis server port (default: 6379)
   --redis-password value  Password to use when connecting to the server
   --data-dir value        Data dir (default: "./data")
   --dist-url value        dist url (default: "/dists/%package%/%reference%.%type%")
   --metadata-url value    MetadataURL (default: "/p2/%package%.json")
   --providers-url value   ProvidersURL (default: "/p/%package%$%hash%.json")
   --dump                  dump all json (default: false)
   -v                      Verbose (default: false)
   --help, -h              show help (default: false)

```
