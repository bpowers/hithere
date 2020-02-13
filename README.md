hithere
=======

[![Build Status](https://travis-ci.org/bpowers/hithere.svg?branch=master)](https://travis-ci.org/bpowers/hithere)


hithere is a tiny program that sends some load to a web application, based on a Python-like script.

hithere is built on [hey](https://github.com/rakyll/hey) and [skycfg](https://github.com/stripe/skycfg).

hey was originally called boom and was influenced from Tarek Ziade's
tool at [tarekziade/boom](https://github.com/tarekziade/boom). Using the same name was a mistake as it resulted in cases
where binary name conflicts created confusion.
To preserve the name for its original owner, we renamed this project to hey.

## Installation

* Linux 64-bit: https://storage.googleapis.com/hey-release/hey_linux_amd64
* Mac 64-bit: https://storage.googleapis.com/hey-release/hey_darwin_amd64
* Windows 64-bit: https://storage.googleapis.com/hey-release/hey_windows_amd64

### Package Managers

macOS:
-  [Homebrew](https://brew.sh/) users can use `brew install hey`.

## Usage

hithere runs provided number of requests in the provided concurrency level and prints stats.

It also supports HTTP2 endpoints.

```
Usage: hithere [options...] <url>

Options:
  -n  Number of requests to run. Default is 200.
  -c  Number of workers to run concurrently. Total number of requests cannot
      be smaller than the concurrency level. Default is 50.
  -q  Rate limit, in queries per second (QPS) per worker. Default is no rate limit.
  -z  Duration of application to send requests. When duration is reached,
      application stops and exits. If duration is specified, n is ignored.
      Examples: -z 10s -z 3m.
  -o  Output type. If none provided, a summary is printed.
      "csv" is the only supported alternative. Dumps the response
      metrics in comma-separated values format.

  -x  HTTP Proxy address as host:port.
  -h2 Enable HTTP/2.

  -host	HTTP Host header.

  -script skycfg script to use as a load generator; URL and HTTP options ignored.

  -disable-compression  Disable compression.
  -disable-keepalive    Disable keep-alive, prevents re-use of TCP
                        connections between different HTTP requests.
  -disable-redirects    Disable following of HTTP redirects
  -cpus                 Number of used cpu cores.
                        (default for current machine is %d cores)
```
