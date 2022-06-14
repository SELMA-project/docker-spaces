# Test

Run

```
./docker-spaces -p 8888 -docker DOCKER-ENGINE-URL -start 1 -stop 2 -remove 3
```

where DOCKER-ENGINE-URL is in any form of
- unix:///var/run/docker.sock
- ssh://user@remote-host
- http://remote-host

Open browser to [http://localhost:8888/x:selmaproject:tts:777:5002/](http://localhost:8888/x:selmaproject:tts:777:5002/)

# ToDo

- Delay connecting TCP session to the container untill it has started (fixed timeout implemented for now)
- 1min stop and 1 hour delete timer set AFTER servicing the TCP connection (fixed timeout implemented for now)
- Scaling on single and multiple hosts, queuing incomming connections when no resources
- SQLite DB for accounts (2022spaces) and state
- Compatibility mode: implement cookie based session tracking
- Compatibility mode for legacy applications with built-in URLs: selmaproject--tts--777--5002--2022spaces.pinitree.com:7788
- Compatibility mode: adjusting the content length parameter in the HTTP header (relevant only for content replace)

# Docker Desktop for macOS Configuration

To enable docker engine connection via SSH to macOS docker host, add the following line

```export PATH=$PATH:/usr/local/bin```

to `~/.bashrc` or `~/.zshenv`.
