#set GOARCH=386
#set GOOS=linux
#go build -o gftp gftp.go
set GOARCH=amd64
set GOOS=windows
go build -o gftp.exe gftp.go

