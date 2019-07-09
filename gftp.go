package main

import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/pkg/sftp"

	"golang.org/x/crypto/ssh"
)

const (
	// PASSWORD - аутентификация по паролю
	PASSWORD = 1
	// PUBKEY — аутентификация по ключу
	PUBKEY = 2
	// DEFTIMEOUT — таймаут
	DEFTIMEOUT = 3 // second
	appname    = "George's FTP"
	appver     = "1.0"
)

// SSH — структура описывающая соединение SSH
type SSH struct {
	IP      string
	User    string
	Cert    string //password or key file path
	Port    int
	session *ssh.Session
	client  *ssh.Client
}

type syncFolder struct {
	Remote string
	Local  string
}

var (
	hostKey       ssh.PublicKey
	host          string
	port          = 22
	sftpuser      string
	rsaPath       string
	remoteFolders []syncFolder
)

func init() {
	version := flag.Bool("v", false, "version")
	flag.Parse()

	if *version {
		fmt.Println(appname, appver)
		os.Exit(0)
	}

	fmt.Println(appname, appver)
	loadEnvironments()
}

func main() {
	client := &SSH{
		IP:   host,
		User: sftpuser,
		Port: port,
		Cert: rsaPath,
	}
	client.connect(PUBKEY)
	client.getFolders()
	client.close()
}

func (ssh_client *SSH) readPublicKeyFile(file string) ssh.AuthMethod {
	buffer, err := ioutil.ReadFile(file)
	if err != nil {
		return nil
	}

	key, err := ssh.ParsePrivateKey(buffer)
	if err != nil {
		return nil
	}
	return ssh.PublicKeys(key)
}

func (ssh_client *SSH) connect(mode int) {

	var sshConfig *ssh.ClientConfig
	var auth []ssh.AuthMethod
	if mode == PASSWORD {
		auth = []ssh.AuthMethod{ssh.Password(ssh_client.Cert)}
	} else if mode == PUBKEY {
		auth = []ssh.AuthMethod{ssh_client.readPublicKeyFile(ssh_client.Cert)}
	} else {
		log.Println("does not support mode: ", mode)
		return
	}

	sshConfig = &ssh.ClientConfig{
		User: ssh_client.User,
		Auth: auth,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return nil
		},
		Timeout: time.Second * DEFTIMEOUT,
	}

	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", ssh_client.IP, ssh_client.Port), sshConfig)
	if err != nil {
		fmt.Println(err)
		return
	}

	session, err := client.NewSession()
	if err != nil {
		fmt.Println(err)
		client.Close()
		return
	}

	ssh_client.session = session
	ssh_client.client = client
}

func (ssh_client *SSH) close() {
	ssh_client.session.Close()
	ssh_client.client.Close()
}

func (ssh_client *SSH) getFolders() {
	client, err := sftp.NewClient(ssh_client.client)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	for _, fld := range remoteFolders {
		err = getFolder(client, fld)
		if err != nil {
			fmt.Println("Error", err)
		}
	}
}

func getFolder(client *sftp.Client, sfld syncFolder) error {
	createIfNotExist(filepath.Join(sfld.Local))
	w := client.Walk(sfld.Remote)
	for w.Step() {
		if w.Err() != nil {
			continue
		}
		fi := w.Stat()
		outPath := strings.TrimPrefix(w.Path(), sfld.Remote)
		if fi.IsDir() {
			if len(outPath) > 0 {
				createIfNotExist(filepath.Join(sfld.Local, outPath))
			}
		} else {
			srcFile, err := client.Open(w.Path())
			if err != nil {
				return err
			}

			allow, err := chkLocalFile(filepath.Join(sfld.Local, outPath), srcFile)
			if err != nil {
				return err
			}

			if allow {
				fmt.Println("Copy", w.Path(), "...")
				dstFile, err := os.Create(filepath.Join(sfld.Local, outPath))
				if err != nil {
					return err
				}
				defer dstFile.Close()

				bytes, err := io.Copy(dstFile, srcFile)
				if err != nil {
					return err
				}
				fmt.Printf("%s copied (%s) \n", w.Path(), byteCountIEC(bytes))

				err = dstFile.Sync()
				if err != nil {
					return err
				}
				srcFile.Close()
			}
		}
	}
	return nil
}

func chkLocalFile(path string, src *sftp.File) (bool, error) {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		fmt.Println("File", path, "not exist")
		return true, nil
	}

	stat, err := src.Stat()
	if err != nil {
		return false, err
	}

	if st.Size() != stat.Size() {
		fmt.Println("Local and remote files have different size. Allow copy.")
		return true, nil
	} else {
		fmt.Println("Local and remote files have igual size. Don't allow copy.")
	}

	return false, nil
}

func byteCountIEC(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB",
		float64(b)/float64(div), "KMGTPE"[exp])
}

func loadEnvironments() {
	envFiles := []string{".env"}
	usr, err := user.Current()
	if err == nil {
		envFiles = append(envFiles, filepath.Join(usr.HomeDir, "backup.env"))
	}

	_ = godotenv.Load(envFiles...)
	// if err != nil {
	// 	log.Fatal("Error:", err)
	// }
	var exists bool

	host, exists = os.LookupEnv("SFTP_HOST")
	if !exists {
		log.Fatal("Not found SFTP_HOST")
	}

	sftpuser, exists = os.LookupEnv("SFTP_USER")
	if !exists {
		log.Fatal("Not found SFTP_USER")
	}

	remotePaths, exists := os.LookupEnv("REMOTE_PATHS")
	if !exists {
		log.Fatal("Not found REMOTE_PATHS")
	}
	remoteFolders = make([]syncFolder, 0)
	remotes := strings.Split(remotePaths, ",")
	for _, remoteFld := range remotes {
		remoteFolders = append(remoteFolders, syncFolder{Remote: remoteFld, Local: "./"})
	}

	localPaths, exists := os.LookupEnv("LOCAL_PATHS")
	if exists {
		localFolders := strings.Split(localPaths, ",")
		for i, fld := range localFolders {
			if len(fld) > 0 {
				remoteFolders[i].Local = fld
			} else {
				remoteFolders[i].Local = "./"
			}
		}
	}

	rsaPath, exists = os.LookupEnv("SFTP_KEY")
	fmt.Println(rsaPath)
	if !exists {
		usr, err := user.Current()
		if err != nil {
			log.Fatal("SFTP Key not explain")
		}
		rsaPath = filepath.Join(usr.HomeDir, ".ssh", "georges_rsa")
	}

	if _, err := os.Stat(rsaPath); os.IsNotExist(err) {
		log.Fatal("SFTP Key no exist!")
	}
}

// CreateIfNotExist - Создание директории, если она не существует
func createIfNotExist(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		os.MkdirAll(path, os.ModePerm)
	}
}
