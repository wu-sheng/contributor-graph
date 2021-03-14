package gcpdb

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"

	"cloud.google.com/go/datastore"
	"github.com/google/go-github/v33/github"

	"github.com/api7/contributor-graph/api/internal/ghapi"
	"github.com/api7/contributor-graph/api/internal/utils"
)

// if repoInput is not empty, fetch single repo and store it in db
// else, use repo list to do daily update for all repos
func UpdateDB(dbCli *datastore.Client, repoInput string) ([]utils.ReturnCon, int, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if dbCli == nil {
		var err error
		dbCli, err = datastore.NewClient(ctx, utils.ProjectID)
		if err != nil {
			return nil, http.StatusInternalServerError, fmt.Errorf("Failed to create client: %v", err)
		}
	}
	defer dbCli.Close()

	ghCli := ghapi.GetGithubClient(ctx, utils.Token)

	var repos []string
	if repoInput == "" {
		fileContent, err := ioutil.ReadFile(utils.RepoPath)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		repos = strings.Split(string(fileContent), "\n")
	} else {
		repos = []string{repoInput}
	}

	for _, repoName := range repos {
		if repoName == "" {
			continue
		}
		log.Println(repoName)

		conGH, code, err := getContributorsNumFromGH(ctx, ghCli, repoName)
		if err != nil {
			return nil, code, err
		}
		conNumGH := len(conGH)

		conNumDB, err := getContributorsNumFromDB(ctx, dbCli, repoName)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}

		if conNumDB == conNumGH {
			log.Printf("Repo no need to update with contributor number %d\n", conNumDB)
			continue
		}

		log.Printf("Repo %s need to update from %d to %d\n", repoName, conNumDB, conNumGH)
		newCons := conGH[conNumDB:]
		cons, code, err := updateContributorList(ctx, dbCli, ghCli, repoName, newCons)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}

		if err := updateRepoList(ctx, dbCli, repoName, conNumGH); err != nil {
			return nil, http.StatusInternalServerError, err
		}

		if repoInput != "" {
			returnCons := make([]utils.ReturnCon, len(cons))
			for i := range cons {
				returnCons[i] = *cons[i]
			}
			return returnCons, http.StatusOK, nil
		}
	}

	return nil, http.StatusOK, nil
}

func getContributorsNumFromGH(ctx context.Context, ghCli *github.Client, repoName string) ([]utils.ConGH, int, error) {
	owner, repo, err := ghapi.SplitRepo(repoName)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}

	cons, code, err := ghapi.GetContributors(ctx, owner, repo, ghCli)
	if err != nil {
		return nil, code, err
	}

	return cons, http.StatusOK, err
}

func getContributorsNumFromDB(ctx context.Context, cli *datastore.Client, repoName string) (int, error) {
	return 0, nil
	repoKey := datastore.NameKey("Repo", repoName, nil)
	repoNum := utils.RepoNum{}
	if err := cli.Get(ctx, repoKey, &repoNum); err != nil {
		if err == datastore.ErrNoSuchEntity {
			return 0, nil
		}
		return 0, err
	}
	return repoNum.Num, nil
}

func updateContributorList(ctx context.Context, dbCli *datastore.Client, ghCli *github.Client, repoName string, newCons []utils.ConGH) ([]*utils.ReturnCon, int, error) {
	owner, repo, err := ghapi.SplitRepo(repoName)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}

	returnCons, code, err := ghapi.GetAndSortCommits(ctx, owner, repo, newCons, ghCli)
	if err != nil {
		return nil, code, err
	}

	keys := make([]*datastore.Key, len(returnCons))
	inKey := datastore.IncompleteKey(repoName, utils.ConParentKey)

	for i := range returnCons {
		keys[i] = inKey
	}

	if _, err := dbCli.PutMulti(ctx, keys, returnCons); err != nil {
		return nil, http.StatusInternalServerError, err
	}

	return returnCons, http.StatusOK, nil
}

func updateRepoList(ctx context.Context, dbCli *datastore.Client, repoName string, conNumGH int) error {
	updatedRepo := &utils.RepoNum{conNumGH}
	key := datastore.NameKey("Repo", repoName, nil)
	if _, err := dbCli.Put(ctx, key, updatedRepo); err != nil {
		return err
	}

	return nil
}
