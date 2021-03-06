package snapshot

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// Postgres creates snapshots for Postgres containers. It dumps the
// database using pg_dump.
type Postgres struct {
	client *client.Client
	dbUser string
}

// NewPostgres creates a new Postgres snapshotter.
func NewPostgres(c *client.Client, dbUser string) Snapshotter {
	return &Postgres{c, dbUser}
}

// Create creates a new snapshot.
func (c *Postgres) Create(ctx context.Context, container types.ContainerJSON, title, imageName string) error {
	buildContext, err := ioutil.TempDir("", "dksnap-context")
	if err != nil {
		return err
	}
	defer os.RemoveAll(buildContext)

	dump, err := exec(ctx, c.client, container.ID, []string{"pg_dump", "-U", c.dbUser})
	if err != nil {
		return err
	}

	if err := ioutil.WriteFile(filepath.Join(buildContext, "dump.sql"), dump, 0644); err != nil {
		return err
	}

	return buildImage(ctx, c.client, buildOptions{
		baseImage: container.Image,
		context:   buildContext,
		buildInstructions: []string{
			"COPY dump.sql /docker-entrypoint-initdb.d/dump.sql",
		},
		title:      title,
		imageNames: []string{imageName},
		dumpPath:   "/docker-entrypoint-initdb.d/dump.sql",
	})
}

func exec(ctx context.Context, dockerClient *client.Client, container string, cmd []string) ([]byte, error) {
	execID, err := dockerClient.ContainerExecCreate(ctx, container, types.ExecConfig{
		Cmd:          cmd,
		AttachStderr: true,
		AttachStdout: true,
	})
	if err != nil {
		return nil, err
	}

	execStream, err := dockerClient.ContainerExecAttach(ctx, execID.ID, types.ExecStartCheck{})
	if err != nil {
		return nil, err
	}
	defer execStream.Close()

	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, execStream.Reader)
	if err != nil {
		return nil, err
	}

	execStatus, err := dockerClient.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return nil, err
	}

	if execStatus.ExitCode != 0 {
		return nil, fmt.Errorf("non-zero exit %d: %s",
			execStatus.ExitCode, stderr.String())
	}
	return stdout.Bytes(), nil
}
