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

### Debugging

* [ ] Cannot connect to the running container immediately (starts a new container each time; after RemoveTimeout deletes only the latest container)
* [ ] ~~startTimeout starts before the container is created (takes 12-20sec)~~

### Features

* [x] Delay connecting TCP session to the container untill it has started (fixed timeout implemented for now)
* [x] 1min stop and 1 hour delete timer set AFTER servicing the TCP connection (fixed timeout implemented for now)
* [ ] Scaling on single and multiple hosts, queuing incomming connections when no resources
* [ ] SQLite DB for accounts (2022spaces), state, config, docker-compose
* [ ] Private DockerHub repositories supported
* [ ] Differentiate 1-thread (x) and multithred (y) containers (different queueing strategy): http://localhost:8888/y:selmaproject:tts:777:5002/

### Optional (alternatively, can be implemented via NGINX)

* [x] Compatibility mode: implement ~~cookie~~ HTTP Referrer based session tracking
* [ ] Compatibility mode for legacy applications with built-in URLs: selmaproject--tts--777--5002--2022spaces.pinitree.com:7788
* [ ] Compatibility mode: implement HTTP content replace and adjust the content length parameter in the HTTP header
* [ ] HTTPS CORS for serverless NLP pipeline calls directly from the Web frontend JavaScript, WASM (e.g. JupyterLight)

### Considerations

* [ ] Image can be force-removed immediately after container is created, but this still leaves a no-name image: docker image rm selmaproject/tts:777 -f; start time CANNOT be much reduced by starting from container rather than creating a new container from image (12-20sec for TTS). Container = Image + TinyDataLayer.
* [ ] Pulling (Downloading & Extracting) a 5.67GB TTS image from DockerHub takes 3:53min => 1GB/1min => (200Mb/s download + decompression)

# Docker Desktop for macOS Configuration

To enable docker engine connection via SSH to macOS docker host, add the following line

```export PATH=$PATH:/usr/local/bin```

to `~/.bashrc` or `~/.zshenv`.
