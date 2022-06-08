# Test

Run

```
./docker-spaces -p 8888 -docker DOCKER-ENGINE-URL
```

where DOCKER-ENGINE-URL is in any form of
- unix:///var/run/docker.sock
- ssh://user@remote-host
- http://remote-host

Open browser to [http://localhost:8888/x:selmaproject:tts:777:5002/](http://localhost:8888/x:selmaproject:tts:777:5002/)

# ToDo

Delay connecting TCP session to the container untill it has started
Compatibility mode for legacy applications (built-in URLs)
Adjusting the content length parameter in the HTTP header
1min stop and 1 hour delete timer set AFTER servicing the TCP connection
Scaling on single and multiple hosts, queuing incomming connections when no resources
