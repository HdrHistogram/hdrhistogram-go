hdrhistogram-go
===============

<a href="https://pkg.go.dev/github.com/HdrHistogram/hdrhistogram-go"><img src="https://pkg.go.dev/badge/github.com/HdrHistogram/hdrhistogram-go" alt="PkgGoDev"></a>
[![Gitter](https://badges.gitter.im/Join_Chat.svg)](https://gitter.im/HdrHistogram/HdrHistogram)
![Test](https://github.com/HdrHistogram/hdrhistogram-go/workflows/Test/badge.svg?branch=master)
 [![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/HdrHistogram/hdrhistogram-go/blob/master/LICENSE)


A pure Go implementation of the [HDR Histogram](https://github.com/HdrHistogram/HdrHistogram).

> A Histogram that supports recording and analyzing sampled data value counts
> across a configurable integer value range with configurable value precision
> within the range. Value precision is expressed as the number of significant
> digits in the value recording, and provides control over value quantization
> behavior across the value range and the subsequent value resolution at any
> given level.

For documentation, check [godoc](http://godoc.org/github.com/codahale/hdrhistogram).

Repo transfer and impact on go dependencies
-------------------------------------------
This repository has been transferred under the github HdrHstogram umbrella with the help from the orginal
author in Sept 2020. The main reasons are to group all implementations under the same roof and to provide more active contribution
from the community as the orginal repository was archived several years ago.
Unfortunately such URL change will break go applications that depend on this library
directly or indirectly.
The dependency URL should be modified to point to the new repository URL.
The tag "v0.9.0" was applied at the point of transfer and will reflect the exact code that was frozen in the
original repository.

Credits
-------

Many thanks for Coda Hale for contributing the initial implementation and transfering the repository here.
