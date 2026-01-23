package scaling_vm

import (
	"fmt"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"io/ioutil"
	"os"
)

const (
	username = "matth"
	//host = "192.168.1.10"
	//port = "22"
	localBinaryPath = "./install/data-proxy"   // 本地 Go 编译后的二进制文件路径
	remotePath      = "/home/matth/data-proxy" // 远程服务器目标路径
)

// SSHConfig 包含 SSH 连接所需的配置信息
type SSHConfig struct {
	Username string
	Host     string
	Port     string
}

// sshToMatthAndDeployBinary SSH 连接到服务器，上传二进制文件并启动它
func sshToMatthAndDeployBinary(config *SSHConfig, localBinaryPath, remotePath string) error {
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

	// 打开远程会话
	session, err := conn.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	// 读取本地二进制文件
	data, err := ioutil.ReadFile(localBinaryPath)
	if err != nil {
		return fmt.Errorf("failed to read binary file: %v", err)
	}

	// 上传二进制文件到远程服务器的 /home/matth 目录
	err = uploadBinaryToRemote(session, data, remotePath)
	if err != nil {
		return fmt.Errorf("failed to upload binary: %v", err)
	}

	// 执行远程命令来启动二进制文件
	err = startBinaryInBackground(session, remotePath)
	if err != nil {
		return fmt.Errorf("failed to start binary: %v", err)
	}

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
func uploadBinaryToRemote(session *ssh.Session, data []byte, remotePath string) error {
	// 在远程服务器上创建文件并写入内容
	err := session.Run(fmt.Sprintf("echo '%s' > %s", data, remotePath))
	if err != nil {
		return fmt.Errorf("failed to upload binary to remote: %v", err)
	}
	return nil
}

// startBinaryInBackground 在远程服务器上启动二进制文件，且不阻塞
func startBinaryInBackground(session *ssh.Session, remotePath string) error {
	// 在远程服务器上进入目标目录并后台启动二进制文件
	cmd := fmt.Sprintf("cd /home/matth && nohup ./data-proxy > /dev/null 2>&1 &")
	err := session.Run(cmd)
	if err != nil {
		return fmt.Errorf("failed to start binary in background: %v", err)
	}
	fmt.Println("Binary started successfully in the background.")
	return nil
}

// deployBinaryToServer 这个函数将配置与文件路径作为输入，执行 SSH 连接、文件上传和启动操作
func deployBinaryToServer(username, host, port, localBinaryPath, remotePath string) error {
	// 创建 SSH 配置
	config := &SSHConfig{
		Username: username,
		Host:     host,
		Port:     port,
	}

	// 调用 SSH 连接并部署二进制文件
	return sshToMatthAndDeployBinary(config, localBinaryPath, remotePath)
}

//func main() {
//	// 示例配置：远程服务器的配置信息
//	username := "matth"
//	host := "192.168.1.10"
//	port := "22"
//	localBinaryPath := "./install/data-proxy" // 本地 Go 编译后的二进制文件路径
//	remotePath := "/home/matth/data-proxy"    // 远程服务器目标路径
//
//	// 调用函数执行任务
//	err := deployBinaryToServer(username, host, port, localBinaryPath, remotePath)
//	if err != nil {
//		log.Fatal(err)
//	}
//}
