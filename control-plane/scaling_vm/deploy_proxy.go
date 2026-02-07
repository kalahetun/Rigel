package scaling_vm

import (
	"fmt"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

const (
	username = "matth"
	//host = "192.168.1.10"
	//port = "22"
	localPathProxy  = "./install/data-proxy"   // 本地 Go 编译后的二进制文件路径
	remotePathProxy = "/home/matth/data-proxy" // 远程服务器目标路径
	binaryProxy     = "./data-proxy"           // 二进制文件名
	localPathPlane  = "./install/data-plane"   // 本地 Go 编译后的二进制文件路径
	remotePathPlane = "/home/matth/data-plane" // 远程服务器目标路径
	binaryPlane     = "./data-plane"           // 二进制文件名
)

// SSHConfig 包含 SSH 连接所需的配置信息
type SSHConfig struct {
	Username string
	Host     string
	Port     string
}

// sshToMatthAndDeployBinary SSH 连接到服务器，上传二进制文件并启动它
func sshToDeployBinary(config *SSHConfig, localPath, remotePath, binaryString, pre string, logger *slog.Logger) error {
	// 创建 SSH 客户端配置，使用系统默认的 SSH 配置
	clientConfig := &ssh.ClientConfig{
		User:            config.Username,
		Auth:            []ssh.AuthMethod{ssh.PublicKeysCallback(agentCallback())}, // 使用默认 SSH 密钥
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),                               // 忽略主机密钥验证（生产环境中应谨慎）
	}

	// 连接到 SSH 服务器
	conn, err := ssh.Dial("tcp", fmt.Sprintf("%s:%s", config.Host, config.Port), clientConfig)
	if err != nil {
		return fmt.Errorf("failed to dial: %v", err)
	}
	defer conn.Close()

	logger.Info("ssh Dial success", slog.String("pre", pre))

	// 打开远程会话
	session, err := conn.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	logger.Info("NewSession success", slog.String("pre", pre))

	// 读取本地二进制文件
	//data, err := ioutil.ReadFile(localBinaryPath)
	//if err != nil {
	//	return fmt.Errorf("failed to read binary file: %v", err)
	//}

	// 上传二进制文件到远程服务器的 /home/matth 目录
	err = UploadDirSFTP(conn, localPath, remotePath)
	if err != nil {
		return fmt.Errorf("failed to upload binary: %v", err)
	}

	logger.Info("UploadDirSFTP success", slog.String("pre", pre))

	// 执行远程命令来启动二进制文件
	err = startBinaryInBackground(session, remotePath, binaryString, logger)
	if err != nil {
		return fmt.Errorf("failed to start binary: %v", err)
	}

	logger.Info("startBinaryInBackground success", slog.String("pre", pre))

	return nil
}

// agentCallback 用于获取默认 SSH agent 中的密钥
func agentCallback() func() ([]ssh.Signer, error) {
	// 返回一个闭包函数，符合 PublicKeysCallback 的要求
	return func() ([]ssh.Signer, error) {
		// 获取 SSH agent
		sshAgent := agent.NewClient(os.Stdin) // 使用 os.Stdin 连接到默认的 SSH agent
		if sshAgent == nil {
			return nil, fmt.Errorf("failed to connect to SSH agent")
		}

		// 获取密钥列表
		keys, err := sshAgent.List()
		if err != nil {
			return nil, fmt.Errorf("failed to list keys: %v", err)
		}

		if len(keys) == 0 {
			return nil, fmt.Errorf("no keys found in the agent")
		}

		// 将 ssh.Key 转换为 ssh.Signer
		var signers []ssh.Signer
		for _, key := range keys {
			signer, err := ssh.NewSignerFromKey(key)
			if err != nil {
				return nil, fmt.Errorf("failed to create signer: %v", err)
			}
			signers = append(signers, signer)
		}

		return signers, nil
	}
}

// uploadBinaryToRemote 上传二进制文件到远程服务器
func UploadDirSFTP(sshClient *ssh.Client, localDir, remoteDir string) error {
	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("create sftp client failed: %w", err)
	}
	defer sftpClient.Close()

	// 确保远端根目录存在
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

		// 打开本地文件
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		// 创建远端文件
		dstFile, err := sftpClient.Create(remotePath)
		if err != nil {
			return err
		}
		defer dstFile.Close()

		// 复制内容
		if _, err := io.Copy(dstFile, srcFile); err != nil {
			return err
		}

		// 保留权限（可选但推荐）
		if err := sftpClient.Chmod(remotePath, info.Mode()); err != nil {
			return err
		}

		return nil
	})
}

// startBinaryInBackground 在远程服务器上启动二进制文件，且不阻塞
func startBinaryInBackground(
	session *ssh.Session,
	remotePath string,
	binaryString string,
	logger *slog.Logger,
) error {

	// 基本防御：避免空值
	if remotePath == "" || binaryString == "" {
		return fmt.Errorf("remotePath or binaryString is empty")
	}

	// 构造安全一点的命令
	cmd := fmt.Sprintf(
		"cd %q && nohup ./%q > /dev/null 2>&1 &",
		remotePath,
		binaryString,
	)

	logger.Info("Starting remote binary",
		"workdir", remotePath,
		"binary", binaryString,
	)

	if err := session.Run(cmd); err != nil {
		return fmt.Errorf("failed to start binary in background: %w", err)
	}

	logger.Info("Binary started successfully in background")
	return nil
}

// deployBinaryToServer 这个函数将配置与文件路径作为输入，执行 SSH 连接、文件上传和启动操作
func deployBinaryToServer(username, host, port, localPath, remotePath, binaryString, pre string, logger *slog.Logger) error {
	// 创建 SSH 配置
	config := &SSHConfig{
		Username: username,
		Host:     host,
		Port:     port,
	}
	// 调用 SSH 连接并部署二进制文件
	return sshToDeployBinary(config, localPath, remotePath, binaryString, pre, logger)
}
