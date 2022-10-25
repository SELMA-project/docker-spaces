#!/bin/sh

#image=selmaproject/uc0:matrix1

image=$1
targetimage=$2

([ -z "$image" ] || [ -z "$targetimage" ]) && echo "usage: $0 [source-image] [target-image]\n\nthis script renews demo.pinitree.com certificate (must be run from demo.pinitree.com host with working letsencrypt certificates)" && exit 1

container=tempcontainer

docker create --name $container $image

sudo docker cp -L /etc/letsencrypt/live/demo.pinitree.com/privkey.pem $container:/app/demo.pinitree.com.key
sudo docker cp -L /etc/letsencrypt/live/demo.pinitree.com/fullchain.pem $container:/app/demo.pinitree.com.crt

docker commit $container $targetimage
docker rm $container
