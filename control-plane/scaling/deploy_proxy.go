package scaling

import (
	"control-plane/util"
	"fmt"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"
)

var (
	username        string
	localPathProxy  string
	remotePathProxy string
	binaryProxy     string
	localPathPlane  string
	remotePathPlane string
	binaryPlane     string
	privateKey      string
)

func InitScalingConfig() {
	uu := util.Config_
	username = uu.Scaling.Username
	localPathProxy = uu.Scaling.LocalPathProxy
	remotePathProxy = uu.Scaling.RemotePathProxy
	binaryProxy = uu.Scaling.BinaryProxy
	localPathPlane = uu.Scaling.LocalPathPlane
	remotePathPlane = uu.Scaling.RemotePathPlane
	binaryPlane = uu.Scaling.BinaryPlane
	privateKey = uu.Scaling.PrivateKey
}

type SSHConfig struct {
	Username   string
	Host       string
	Port       string
	PrivateKey string
}

func sshToDeployBinary(config *SSHConfig, localPath_, remotePath_, binaryString_, pre string, logger *slog.Logger) error {

	logger.Info("SshToDeployBinary", slog.String("pre", pre))

	//
	key, err := os.ReadFile(config.PrivateKey)
	if err != nil {
		return fmt.Errorf("read private key failed: %v", err)
	}

	logger.Info("Read private key success", slog.String("pre", pre),
		slog.String("binaryString", binaryString_))

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return fmt.Errorf("parse private key failed: %v", err)
	}

	logger.Info("Parse private key success", slog.String("pre", pre),
		slog.String("binaryString", binaryString_))

	clientConfig := &ssh.ClientConfig{
		User: config.Username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	//
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	tcpConn, err := dialer.Dial("tcp", net.JoinHostPort(config.Host, config.Port))
	if err != nil {
		return fmt.Errorf("failed to dial TCP: %v", err)
	}

	logger.Info("Tcp Dial success", slog.String("pre", pre),
		slog.String("binaryString", binaryString_))

	//
	conn, chans, reqs, err := ssh.NewClientConn(tcpConn, net.JoinHostPort(config.Host, config.Port), clientConfig)
	if err != nil {
		return fmt.Errorf("failed to create SSH client: %v", err)
	}
	defer conn.Close()

	logger.Info("Ssh Dial success", slog.String("pre", pre),
		slog.String("binaryString", binaryString_))

	client := ssh.NewClient(conn, chans, reqs)
	defer client.Close()

	logger.Info("Ssh new NewClient", slog.String("pre", pre),
		slog.String("binaryString", binaryString_))

	//
	err = UploadDirSFTP(client, localPath_, remotePath_)
	if err != nil {
		return fmt.Errorf("failed to upload binary: %v", err)
	}
	logger.Info("UploadDirSFTP success", slog.String("pre", pre),
		slog.String("binaryString", binaryString_))

	//
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	logger.Info("NewSession success", slog.String("pre", pre),
		slog.String("binaryString", binaryString_))

	err = startBinaryInBackground(session, remotePath_, binaryString_, pre, logger)
	if err != nil {
		return fmt.Errorf("failed to start binary: %v", err)
	}
	logger.Info("StartBinaryInBackground success", slog.String("pre", pre),
		slog.String("binaryString", binaryString_))

	return nil
}

// uploadBinaryToRemote 上传二进制文件到远程服务器
func UploadDirSFTP(sshClient *ssh.Client, localDir, remoteDir string) error {

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("create sftp client failed: %w", err)
	}
	defer sftpClient.Close()

	if err := sftpClient.MkdirAll(remoteDir); err != nil {
		return fmt.Errorf("create remote dir failed: %w", err)
	}

	return filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(localDir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		remotePath := filepath.Join(remoteDir, relPath)

		if info.IsDir() {
			// 创建远端目录
			return sftpClient.MkdirAll(remotePath)
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := sftpClient.Create(remotePath)
		if err != nil {
			return err
		}
		defer dstFile.Close()

		if _, err := io.Copy(dstFile, srcFile); err != nil {
			return err
		}

		if err := sftpClient.Chmod(remotePath, info.Mode()); err != nil {
			return err
		}

		return nil
	})
}

// startBinaryInBackground 在远程服务器上启动二进制文件，且不阻塞
func startBinaryInBackground(session *ssh.Session, remotePath_ string, binaryString_ string, pre string, logger *slog.Logger) error {

	if remotePath_ == "" || binaryString_ == "" {
		return fmt.Errorf("remotePath or binaryString is empty")
	}

	cmd := fmt.Sprintf(
		`cd %q && test -x %q && nohup ./%q > nohup.out 2>&1 < /dev/null & >/dev/null 2>&1`,
		remotePath_,
		binaryString_,
		binaryString_,
	)

	logger.Info("Starting remote binary",
		slog.String("pre", pre),
		slog.String("workdir", remotePath_),
		slog.String("binary", binaryString_),
		slog.String("cmd", cmd),
	)

	errCh := make(chan error, 1)

	go func() {
		errCh <- session.Run(cmd)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("failed to start binary in background: %w", err)
		}
		logger.Info("Binary started (ssh session returned)",
			slog.String("pre", pre),
			slog.String("binary", binaryString_),
		)

	case <-time.After(3 * time.Second):
		logger.Warn("SSH session did not return, assume binary started",
			slog.String("pre", pre),
			slog.String("binary", binaryString_),
		)
	}

	return nil
}

func deployBinaryToServer(username, host, port, localPath, remotePath, binaryString, pre string, logger *slog.Logger) error {
	config := &SSHConfig{
		Username:   username,
		Host:       host,
		Port:       port,
		PrivateKey: privateKey,
	}
	return sshToDeployBinary(config, localPath, remotePath, binaryString, pre, logger)
}
