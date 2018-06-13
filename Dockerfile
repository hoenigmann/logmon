#docker build -t hoenigmann/golang-docker-logmon .
#docker images
#docker run --rm -it hoenigmann/golang-docker-logmon .
FROM golang:1.9
WORKDIR /go/src/github.com/hoenigmann/logmon/
COPY main.go .
RUN go get ./...
RUN touch /var/log/access.log
RUN go build -o logmon .
ENTRYPOINT logmon
