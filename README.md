# Test

Run

```
./docker-spaces -p 8888 -docker DOCKER-ENGINE-URL --user USER --password PASSWORD
./docker-spaces -p 8888 -registry https://dockers.priberam.com --user dev --password VYD7C.KU#IPO-5ex

./docker-spaces -p 1100 -registry https://dockers.priberam.com --user dev --password VYD7C.KU#IPO-5ex -source 10 -cluster 19100:5:http://172.25.1.547878/docker:local -cluster 12100:5:unix:///var/run/docker.sock

localhost:8888/x:dockers.priberam.com:postgres:12:3/

http://localhost:8888/x:dockers.priberam.com:worker-sidecar:dockerspaces:3:RabbitMQ__Url=amqp%3A%2F%2F172.25.1.54%3A8672;SidecarWorker__WorkerType=FullReindexer;ReindexerSettings__Url=http%3A%2F%2Fpbanet05-22.interno.priberam.pt%3A80%2F;ReindexerSettings__Scenario=SUNSET6;InputQueue__Name=Indexer.en/

http://localhost:8888/x:dockers.priberam.com:docker-tutorial:0.0.1:5006/
```

where DOCKER-ENGINE-URL is in any form of
- unix:///var/run/docker.sock (default)
- ssh://user@remote-host
- http://remote-host

Open browser to [http://localhost:8888/x:selmaproject:tts:777:5002/](http://localhost:8888/x:selmaproject:tts:777:5002/)

# ToDo

### Debugging


### Features

* [x] Scaling on multiple hosts in the cluster, example:  `./docker-spaces.linux.x86_64 -p 1100 -source 500 -cluster 19100:50:http://111.111.111.111:7878/docker:local -cluster 12100:5:unix:///var/run/docker.sock`

* [x] Run docker-spaces inside Docker container: `docker run -p 44222:8888 -v /var/run/docker.sock:/var/run/docker.sock  --restart=always selmaproject/uc0:spaces20 --user USER --password PASSWORD`

* [x] GPU support added. When running with the `--gpu true` flag, all running containers will be allocated a separate GPU. This means that `-target int` flag must match the number of GPUs available on each host in the cluster: `./docker-spaces.linux.x86_64 -p 1100 --user USER --password PASSWORD -gpu -target 2` 

* [x] Rabbit MQ worker dynamic scaling support added. A new parameter `--stop 60` is introduced to specify the delay in seconds between SIGTERM and SIGKILL signals sent to the redundant worker containers when docker-spaces scheduler decides to stop them. Another new parameter `--release 1800` specifies in seconds the minimum duration a new worker container instance will be kept running (if docker-spaces has free resources, worker will be allowed to run longer; if docker-spaces is out of resources, a request to create a new worker container will be ignored)

* [x] HTTPS CORS support for frontend NLP pipeline calls directly from the Web frontend JavaScript, WASM (e.g. JupyterLight):
`./docker-spaces.linux.x86_64 -p 1100 -tls -cors -cert cert.pem -key privkey.pem --user USER --password PASSWORD`

* [x] Monitoring of the Docker-spaces internal resource broker added: http://localhost:8888/monitor:broker/ (returns a JSON)

* [ ] SQLite DB for accounts (2022spaces), state, config, docker-compose



### Considerations

* [ ] Pulling (Downloading & Extracting) a 5.67GB TTS image from DockerHub takes 3:53min => 1GB/1min => (200Mb/s download + decompression)

# Docker Desktop for macOS Configuration

To enable docker engine connection via SSH to macOS docker host, add the following line

```export PATH=$PATH:/usr/local/bin```

to `~/.bashrc` or `~/.zshenv`.

# Queueing logic

* x-type jobs are queued and executed one-at-the-time on the container; multiple containers are automatically started to handle a heavy queue.
* y-type jobs are not queued and are immediately connected to the single shared container, which is automatically started on the first request.
* host-type jobs are not queued and are immediately connected to the specified host:port [http://localhost:8888/host:centola.pinitree.com:80/](http://localhost:8888/host:centola.pinitree.com:80/). Container must be started/stopped manually, e.g. with `DOCKER_HOST=tcp://111.111.111.111:7878/docker:local/ docker-compose up -d` Host-type jobs are useful for containers mounting volumes from the host; they can also be debugged with standard docker CLI: `DOCKER_HOST=tcp://111.111.111.111:7878/docker:local/ docker ps`
* RabbitMQ worker dynamic scaling is implemented via extended x-type job syntax REST API call: http://localhost:8888/x:selmaproject:tts:777:3:PARAM1=abc;PARAM2=xyz/ where "3" is a mandatory port number NOT USED by the worker container and PARAM1, PARAM2,... are ENV parameters passed to the worker container (e.g. URL of the Rabbit MQ endpoint). Multiple REAST API calls will result in bringing up multiple worker container instances.
The REST API call must be triggered by external code upon sensing a non-empty RabbitMQ queue via PEEK or other means. The REST API call will return an empty response as soon as the new worker is launched. If docker-spaces is short of resources, this call may stay open for up to 30 minutes while waiting for resources to appear (in this case it is up to the external code to decide how long to wait for the resources to not be too greedy with respect to other queues, which might also be waiting for resources). 
* The above described RabbitMQ worker scaling feature (specifyinig port "3") can be used also for other purposes, e.g., for running batch-jobs such as neural network training (with or without GPU), as jobs started in this manner are guaranteed to run for the duration specified in the "-release" command-line parameter (default is 1800 seconds = 30 minutes), which in this use-case might need to be set to a higher value. 

# Experimental

Experimental DockerSpaces-NG version supporting complete frontend pipelines is available as docker image. This docker image besides DockerSpaces includes also a web-server (PiniTree server) from where the initial frontend webpage is loaded.

To test, run:

```
docker run --restart=always -d -v /var/run/docker.sock:/var/run/docker.sock -p 12555:8888 -it selmaproject/docker-spaces:next3 -user yourname -password yourpassword -start-port 12440 -source 15 -gpu 2 -target 4 -cors
```
and then go to URL http://localhost:12555
The frontend pipeline there is demonstrated via WHISPER button (other buttons still use a shared backend container)

Note: set the parameter "-gpu 2" to 0 if you do not have any GPU, or your CUDA version does not match requirements of CUDA-enabled Docker containers. This parameter tells how many GPUs on your computer should be used by DockerSpaces. Each GPU can be assigned several CUDA-enabled Docker images in round-robin fashion, if GPU RAM capacity is sufficient. DockerSpaces assumes that a CUDA-enabled docker container will not start (will produce an error) if there is not sufficient amount of free RAM on GPU - please ensure that your CUDA-enabled containers do not request additional GPU RAM later in their lifetime (potentially causig no-RAM crash and loss of data being processed).

Note: "docker update --restart=always container-id" can be applied also on an already running container, to make it restart automatically on re-boot. This closely simulates a virtual machine with permanet storage.
