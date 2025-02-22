package poller

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/avast/retry-go/v4"
	"github.com/getnoops/ops/pkg/brain"
)

type PushDockerImageCommandInfo struct {
	ArtifactId string `json:"artifactId"`

	Img string `json:"img"`

	Tag string `json:"tag"`

	DeploymentId string `json:"deploymentId"`

	Type brain.PollerQueueEntryCmdType `json:"type"`
}

func formatDockerCommandInfo(commandMsg string) (*PushDockerImageCommandInfo, error) {
	var dockerCommandInfo PushDockerImageCommandInfo

	err := json.Unmarshal([]byte(commandMsg), &dockerCommandInfo)
	if err != nil {
		return nil, err
	}

	return &dockerCommandInfo, nil
}

func tagDockerImageWithEcrUrl(dockerCommandInfo *PushDockerImageCommandInfo, dockerLogin *brain.DockerLoginResponse) error {
	userProvidedImage := fmt.Sprintf("%s:%s", dockerCommandInfo.Img, dockerCommandInfo.Tag)
	fmt.Printf("Tagging image [%s] with [%s]", userProvidedImage, dockerLogin.Url)

	cmd := exec.Command("docker", "tag", userProvidedImage, dockerLogin.Url)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		return err
	}

	return nil
}

func loginToDocker(dockerLogin *brain.DockerLoginResponse) error {
	// TODO: Investigate if there's a better way to add the password here.
	// ``WARNING! Using --password via the CLI is insecure. Use --password-stdin
	fmt.Println("\nLogging in to docker")

	registryUrl := fmt.Sprintf("https://%s", dockerLogin.Url)

	cmd := exec.Command("docker", "login", "--username", dockerLogin.UserName, "--password", dockerLogin.Password, registryUrl)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		return err
	}

	return nil
}

func pushImage(ecrUrl string) error {
	cmd := exec.Command("docker", "push", ecrUrl)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		return err
	}

	return nil
}

func pushImageWithRetry(ctx context.Context, deploymentId, ecrUrl string) error {
	err := retry.Do(
		func() error {
			err := pushImage(ecrUrl)
			return err
		},
		retry.Attempts(3),
		retry.OnRetry(func(n uint, err error) {
			log.Printf("Unable to push docker image to ECR. Retrying request after error: %v", err)
		}),
	)
	if err != nil {
		e := err.Error()
		notifyDockerUploadBody := brain.NotifyUploadCompleteRequest{Success: false, Error: &e}
		brain.Client.NotifyDockerUploadCompleted(ctx, deploymentId, notifyDockerUploadBody)
		return err
	}

	return nil
}

func pushDockerImageToECR(ctx context.Context, command *brain.PollerQueueEntry, deploymentId string) error {
	fmt.Println("\nStarting process to push your docker image to ECR...")

	dockerCommandInfo, err := formatDockerCommandInfo(command.Command)
	if err != nil {
		return err
	}

	dockerLogin, err := makeRequestToDockerLoginEndpoint(ctx, deploymentId, dockerCommandInfo.ArtifactId)
	if err != nil {
		return err
	}

	err = tagDockerImageWithEcrUrl(dockerCommandInfo, dockerLogin)
	if err != nil {
		return err
	}

	err = loginToDocker(dockerLogin)
	if err != nil {
		return err
	}

	err = pushImageWithRetry(ctx, deploymentId, dockerLogin.Url)
	if err != nil {
		return err
	}

	fmt.Println("\nSuccessfully pushed your image to ECR!")

	notifyUploadCompleteBody := brain.NotifyUploadCompleteRequest{Success: true}
	_, err = brain.Client.NotifyDockerUploadCompleted(ctx, deploymentId, notifyUploadCompleteBody)
	if err != nil {
		return err
	}

	fmt.Println("\nBrain notified that Docker Image has been uploaded.")

	return nil
}
