package torrent

import (
	"errors"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"log"
	"net"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
)

type SftpFsProvider struct {
	Server     string
	Username   string
	Password   string
	ServerPath string
}

//Connection string:  username:password@example.com:8042/over/there/
func NewSftpFsProvider(connection string) SftpFsProvider {
	connSA := strings.Split(connection, "@")
	authSA := strings.Split(connSA[0], ":")
	serverSA := strings.SplitN(connSA[1], "/", 2)
	path := "/"
	if len(serverSA) == 2 { // {example.com:8042, over/there/}
		path += serverSA[1]
	}
	sp := SftpFsProvider{
		Username:   authSA[0],
		Password:   authSA[1],
		Server:     serverSA[0],
		ServerPath: path,
	}
	return sp
}

func (o SftpFsProvider) NewFS(directory string) (fs FileSystem, err error) {
	sftpfs := &SftpFileSystem{
		sp:               o,
		torrentDirectory: directory,
		closed:           true,
	}

	return sftpfs, sftpfs.Connect()
}

type SftpFileSystem struct {
	sp               SftpFsProvider
	torrentDirectory string
	sftpClient       *sftp.Client
	sshClient        *ssh.Client
	closed           bool //false normally, true if closed
}

func (sfs *SftpFileSystem) Connect() error {
	var auths []ssh.AuthMethod
	if aconn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); err == nil {
		auths = append(auths, ssh.PublicKeysCallback(agent.NewClient(aconn).Signers))
	}
	if len(sfs.sp.Password) != 0 {
		auths = append(auths, ssh.Password(sfs.sp.Password))
	}

	config := ssh.ClientConfig{
		User: sfs.sp.Username,
		Auth: auths,
	}
	var err error
	sfs.sshClient, err = ssh.Dial("tcp", sfs.sp.Server, &config)
	if err != nil {
		log.Printf("unable to connect to [%s]: %v", sfs.sp.Server, err)
		return err
	}

	sfs.sftpClient, err = sftp.NewClient(sfs.sshClient)
	if err != nil {
		log.Printf("unable to start sftp subsytem: %v", err)
		return err
	}
	return nil
}

func (sfs *SftpFileSystem) translate(name []string) string {
	path := pathpkg.Clean(sfs.torrentDirectory + "/" + pathpkg.Join(name...))
	return pathpkg.Clean(filepath.Join(sfs.sp.ServerPath, path))
}

func (sfs *SftpFileSystem) Open(name []string, length int64) (File, error) {
	fullPath := sfs.translate(name)
	err := sfs.ensureDirectory(fullPath)
	if err != nil {
		log.Println("Couldn't ensure directory:", fullPath)
		return nil, err
	}

	file, err := sfs.sftpClient.OpenFile(fullPath, os.O_RDWR)
	if err != nil {
		file, err = sfs.sftpClient.Create(fullPath)
		if err != nil {
			log.Println("Couldn't create file:", fullPath, "error:", err)
			return nil, err
		}
	}
	retVal := &SftpFile{file}
	err = file.Truncate(length)
	return retVal, err
}

func (sfs *SftpFileSystem) Close() (err error) {
	sfs.closed = true
	err = sfs.sftpClient.Close()
	sfs.sshClient.Close()
	if err != nil {
		log.Println("Error closing sftp client:", err)
	}
	return
}

func (sfs *SftpFileSystem) ensureDirectory(fullPath string) error {
	fullPath = filepath.ToSlash(fullPath)
	fullPath = pathpkg.Clean(fullPath)
	path := strings.Split(fullPath, "/")
	path = path[:len(path)-1] //remove filename

	total := ""
	for _, str := range path {
		total += str + "/"
		sfs.sftpClient.Mkdir(total)
		//We're not too concerned with if Mkdir gave an error, since it could just be that the
		//directory already exists. And if not, then the final Stat call will error out anyway.
	}
	fi, err := sfs.sftpClient.Lstat(total)
	if err != nil {
		return err
	}

	if fi.IsDir() {
		return nil
	}

	return errors.New("Part of path isn't a directory! path=" + total)
}

type SftpFile struct {
	file *sftp.File
}

func (sff *SftpFile) ReadAt(p []byte, off int64) (n int, err error) {
	sff.file.Seek(off, os.SEEK_SET)
	return sff.file.Read(p)
}

func (sff *SftpFile) WriteAt(p []byte, off int64) (n int, err error) {
	sff.file.Seek(off, os.SEEK_SET)
	return sff.file.Write(p)
}

func (sff *SftpFile) Close() error {
	return sff.file.Close()
}
