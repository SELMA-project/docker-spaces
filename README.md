# Test

Run

```
./docker-spaces -p 8888 -docker DOCKER-ENGINE-URL --user USER --password PASSWORD
```

where DOCKER-ENGINE-URL is in any form of
- unix:///var/run/docker.sock
- ssh://user@remote-host
- http://remote-host

Open browser to [http://localhost:8888/x:selmaproject:tts:777:5002/](http://localhost:8888/x:selmaproject:tts:777:5002/)

# ToDo

### Debugging


### Features

* [ ] Scaling on multiple hosts
* [ ] SQLite DB for accounts (2022spaces), state, config, docker-compose

### Optional (alternatively, can be implemented via NGINX)

* [ ] HTTPS CORS for serverless NLP pipeline calls directly from the Web frontend JavaScript, WASM (e.g. JupyterLight)

### Considerations

* [ ] Pulling (Downloading & Extracting) a 5.67GB TTS image from DockerHub takes 3:53min => 1GB/1min => (200Mb/s download + decompression)

# Docker Desktop for macOS Configuration

To enable docker engine connection via SSH to macOS docker host, add the following line

```export PATH=$PATH:/usr/local/bin```

to `~/.bashrc` or `~/.zshenv`.

# Queueing logic

* x-type jobs are queued and executed one-at-the-time on the container; multiple containers are automatically started to handle a heavy queue.
* y-type jobs are not queued and are immediately connected to the single shared container, which is automatically started on the first request.
* h-type jobs are not queued and are immediately connected to the specified host:port (container must be started/stopped manually, e.g. with "docker-compose up -d"; this is useful for containers mounting volumes from host).
