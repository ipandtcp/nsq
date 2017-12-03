## 简介
该项目主要是学习用，主要是在自己理解的地方添加些注释。如有您发现有理解不正确的地方，恳请指正


<p align="center">
<img align="left" width="175" src="http://nsq.io/static/img/nsq_blue.png">
<ul>
<li><strong>Source</strong>: https://github.com/nsqio/nsq
<li><strong>Issues</strong>: https://github.com/nsqio/nsq/issues
<li><strong>Mailing List</strong>: <a href="https://groups.google.com/d/forum/nsq-users">nsq-users@googlegroups.com</a>
<li><strong>IRC</strong>: #nsq on freenode
<li><strong>Docs</strong>: http://nsq.io
<li><strong>Twitter</strong>: <a href="https://twitter.com/nsqio">@nsqio</a>
</ul>
</p>

[![Build Status](https://secure.travis-ci.org/nsqio/nsq.svg?branch=master)](http://travis-ci.org/nsqio/nsq) [![GitHub release](https://img.shields.io/github/release/nsqio/nsq.svg)](https://github.com/nsqio/nsq/releases/latest) [![Coverage Status](https://coveralls.io/repos/github/nsqio/nsq/badge.svg?branch=master)](https://coveralls.io/github/nsqio/nsq?branch=master)

**NSQ** is a realtime distributed messaging platform designed to operate at scale, handling
billions of messages per day.

It promotes *distributed* and *decentralized* topologies without single points of failure,
enabling fault tolerance and high availability coupled with a reliable message delivery
guarantee.  See [features & guarantees][features_guarantees].

Operationally, **NSQ** is easy to configure and deploy (all parameters are specified on the command
line and compiled binaries have no runtime dependencies). For maximum flexibility, it is agnostic to
data format (messages can be JSON, MsgPack, Protocol Buffers, or anything else). Official Go and
Python libraries are available out of the box (as well as many other [client
libraries][client_libraries]) and, if you're interested in building your own, there's a [protocol
spec][protocol].

We publish [binary releases][installing] for linux, darwin, freebsd and windows as well as an official [Docker image][docker_deployment].

NOTE: master is our *development* branch and may not be stable at all times.


## Authors

NSQ was designed and developed by Matt Reiferson ([@imsnakes][snakes_twitter]) and Jehiah Czebotar
([@jehiah][jehiah_twitter]) but wouldn't have been possible without the support of
[bitly][bitly] and all our [contributors][contributors].

Logo created by Wolasi Konu ([@kisalow][wolasi_twitter]).

[protocol]: http://nsq.io/clients/tcp_protocol_spec.html
[installing]: http://nsq.io/deployment/installing.html
[docker_deployment]: http://nsq.io/deployment/docker.html
[snakes_twitter]: https://twitter.com/imsnakes
[jehiah_twitter]: https://twitter.com/jehiah
[bitly]: https://bitly.com
[features_guarantees]: http://nsq.io/overview/features_and_guarantees.html
[contributors]: https://github.com/nsqio/nsq/graphs/contributors
[client_libraries]: http://nsq.io/clients/client_libraries.html
[wolasi_twitter]: https://twitter.com/kisalow
