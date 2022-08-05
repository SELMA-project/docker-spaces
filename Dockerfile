FROM ubuntu:latest

WORKDIR /app
COPY docker-spaces.linux.x86_64 /app/
RUN chmod +x /app/docker-spaces.linux.x86_64

#VOLUME /app/data
#VOLUME /app/plugins

EXPOSE 8888

#CMD ["sh", "-c", "/app/docker-spaces.linux.x86_64 -p 9001 -source 50 -target 10 -start-port 29700"]
ENTRYPOINT ["/app/docker-spaces.linux.x86_64", "-p", "8888"]
