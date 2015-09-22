package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/pkg/stringid"
	"github.com/spf13/cobra"
)

var (
	verbose  bool
	graphdir string
	driver   string

	ErrNoGraphDriver = errors.New("no graph driver set")
	ErrNeedMigration = errors.New("migration needed")
)

func main() {
	cmd := &cobra.Command{
		Use:   "graphutil",
		Short: "Utility for operating on the docker graph",
		Long:  "",
	}
	cmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	cmd.PersistentFlags().StringVarP(&graphdir, "graph", "g", "/var/lib/docker", "docker graph directory")
	cmd.PersistentFlags().StringP("driver", "s", "overlay", "docker graph driver")

	scrambleCommand := &cobra.Command{
		Use:   "scramble",
		Short: "Scrambles image IDs in the graph directory",
		Long:  "",
		Run:   runScramble,
	}

	downgradeCommand := &cobra.Command{
		Use:   "downgrade",
		Short: "Downgrades utility to be compatible with older version of Docker",
		Long:  "",
		Run:   runDowngrade,
	}
	// TODO: Add flag for version

	cmd.AddCommand(scrambleCommand, downgradeCommand)

	cmd.Execute()
}

func globalFlags(cmd *cobra.Command) {
	if verbose {
		logrus.SetLevel(logrus.DebugLevel)
	}
	if cmd.Flag("driver").Changed {
		driver = cmd.Flag("driver").Value.String()
	} else {
		driver = os.Getenv("DOCKER_GRAPHDRIVER")
	}
}

func getCacheDir(image string) (string, error) {
	if driver == "" {
		return "", ErrNoGraphDriver
	}
	cacheBytes, err := ioutil.ReadFile(filepath.Join(graphdir, "graph", image, "cache-id"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNeedMigration
		}
		return "", err
	}
	cacheID := strings.TrimSpace(string(cacheBytes))

	return filepath.Join(graphdir, driver, cacheID), nil
}

func updateReferences(mapping map[string]string, paths []string) {
	r := regexp.MustCompile(`"[a-fA-F0-9]{64}"`)

	for _, filePath := range paths {
		content, err := ioutil.ReadFile(filePath)
		if err != nil {
			logrus.Errorf("Error reading file %s: %s", filePath, err)
			continue
		}

		indexes := r.FindAllIndex(content, -1)
		if len(indexes) == 0 {
			logrus.Debugf("No matches found in %s", filePath)
			continue
		}
		logrus.Debugf("Found %d matches in %s", len(indexes), filePath)
		var changed int
		for _, rng := range indexes {
			if rng[1]-rng[0] != 66 {
				logrus.Errorf("Bad range %s: %d %d", filePath, rng[0], rng[1])
				continue
			}

			foundID := string(content[rng[0]+1 : rng[1]-1])
			if newID, ok := mapping[foundID]; ok {
				changed++
				if n := copy(content[rng[0]+1:rng[1]-1], []byte(newID)); n != 64 {
					logrus.Errorf("Bad copy on %s: wrote %d bytes", filePath, n)
				}
			}

		}
		if changed > 0 {
			if err := ioutil.WriteFile(filePath, content, 0600); err != nil {
				logrus.Errorf("Error writing file %s: %s", filePath, err)
			}
			logrus.Debugf("Updated %s with %d changes", filePath, changed)
		}
	}
}

func runScramble(cmd *cobra.Command, args []string) {
	globalFlags(cmd)

	t1 := time.Now()
	dir, err := ioutil.ReadDir(filepath.Join(graphdir, "graph"))
	if err != nil {
		logrus.Fatalf("Error reading graph dir: %s", err)
	}
	var ids = []string{}
	for _, v := range dir {
		id := v.Name()
		if len(id) != 64 {
			logrus.Debugf("Skipping: %s", v.Name())
			continue
		}

		cacheDir, err := getCacheDir(id)
		if err != nil {
			if err == ErrNeedMigration {
				logrus.Debugf("%s not migrated", id)
			}
			logrus.Fatalf("Error getting image IDs: %s", err)
		}

		if _, err := os.Stat(cacheDir); err != nil {
			if os.IsNotExist(err) {
				logrus.Debugf("Skipping, missing cache dir: %s", id)
				continue
			}
			logrus.Fatalf("Error checking cache dir %s: %s", cacheDir, err)
		}

		ids = append(ids, id)
	}

	updates := map[string]string{}
	fileUpdates := []string{
		filepath.Join(graphdir, fmt.Sprintf("repositories-%s", driver)),
	}
	for _, id := range ids {
		fmt.Fprintf(cmd.Out(), "Scrambling %s\n", id)

		newID := stringid.GenerateRandomID()
		updates[id] = newID

		oldPath := filepath.Join(graphdir, "graph", id)
		newPath := filepath.Join(graphdir, "graph", newID)
		if err := os.Rename(oldPath, newPath); err != nil {
			logrus.Errorf("Error renaming %s to %s: %s", oldPath, newPath, err)
			continue
		}

		updates[id] = newID
		fileUpdates = append(fileUpdates, filepath.Join(graphdir, "graph", newID, "json"))
	}

	updateReferences(updates, fileUpdates)

	logrus.Debugf("Ran scramble in %s", time.Since(t1).String())
}

func runDowngrade(cmd *cobra.Command, args []string) {
	globalFlags(cmd)

	t1 := time.Now()
	dir, err := ioutil.ReadDir(filepath.Join(graphdir, "graph"))
	if err != nil {
		logrus.Fatalf("Error reading graph dir: %s", err)
	}

	updates := map[string]string{}
	fileUpdates := []string{
		filepath.Join(graphdir, fmt.Sprintf("repositories-%s", driver)),
	}
	for _, v := range dir {
		id := v.Name()
		if len(id) != 64 {
			logrus.Debugf("Skipping: %s", v.Name())
			continue
		}

		cacheDir, err := getCacheDir(id)
		if err != nil {
			if err == ErrNeedMigration {
				logrus.Debugf("%s not migrated", id)
			}
			logrus.Fatalf("Error getting image IDs: %s", err)
		}

		if _, err := os.Stat(cacheDir); err != nil {
			if os.IsNotExist(err) {
				logrus.Debugf("Skipping, missing cache dir: %s", id)
				continue
			}
			logrus.Fatalf("Error checking cache dir %s: %s", cacheDir, err)
		}

		cacheID := filepath.Base(cacheDir)
		if cacheID != id {
			logrus.Debugf("Moving %s back to %s", id, cacheID)
			updates[id] = cacheID

			oldPath := filepath.Join(graphdir, "graph", id)
			newPath := filepath.Join(graphdir, "graph", cacheID)
			if err := os.Rename(oldPath, newPath); err != nil {
				logrus.Errorf("Error renaming %s to %s: %s", oldPath, newPath, err)
				continue
			}
			fileUpdates = append(fileUpdates, filepath.Join(newPath, "json"))
		}
	}

	updateReferences(updates, fileUpdates)

	logrus.Debugf("Ran downgrade in %s", time.Since(t1).String())
}
