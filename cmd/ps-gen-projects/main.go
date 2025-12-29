package main

import (
	"context"
	"flag"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo"
	"github.com/google/go-github/v68/github"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

// Output
type OutputProject struct {
	URL         string   `yaml:"url"`
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Language    string   `yaml:"language"`
	License     string   `yaml:"license"`
	Date        string   `yaml:"date"`
	Topics      []string `yaml:"topics"`
	Stars       int      `yaml:"stars"`
	Forks       int      `yaml:"forks"`
	Issues      int      `yaml:"issues"`
	Icon        string   `yaml:"icon"`
	ForgeDomain string   `yaml:"forgeDomain"`
	ForgeEmoji  string   `yaml:"forgeEmoji"`
}

type OutputCategory struct {
	Title    string          `yaml:"title"`
	Projects []OutputProject `yaml:"projects"`
}

// Input
type InputCategory struct {
	Title    string         `yaml:"title"`
	Projects []InputProject `yaml:"projects"`
}

type InputProject struct {
	Repo string `yaml:"repo"`
	Icon string `yaml:"icon"`
}

// Forges
type ForgeType string

const (
	ForgeTypeGitHub  ForgeType = "github"
	ForgeTypeForgejo ForgeType = "forgejo"
)

type ForgeConfig struct {
	Domain string    `yaml:"domain"`
	Type   ForgeType `yaml:"type"`
	API    string    `yaml:"api"`
	CDN    string    `yaml:"cdn"`
	Emoji  string    `yaml:"emoji"`
}

type ForgeSecret struct {
	Domain string `yaml:"domain"`
	Token  string `yaml:"token"`
}

func main() {
	verbosity := flag.String("verbosity", "info", "Log level (debug, info, warn, error)")
	projectsFile := flag.String("projects", "projects.yaml", "Projects configuration file")
	forgesFile := flag.String("forges", "forges.yaml", "Forges configuration file")
	secretsFile := flag.String("secrets", "secrets.yaml", "Secrets configuration file")
	user := flag.String("user", "pojntfx", "Default username to omit from display (can also be set using the FORGE_USER env variable)")

	flag.Parse()

	var level slog.Level
	if err := level.UnmarshalText([]byte(*verbosity)); err != nil {
		panic(err)
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))

	if userEnv := os.Getenv("FORGE_USER"); userEnv != "" {
		*user = userEnv
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Info("Reading forges configuration", "file", *forgesFile)

	forgesData, err := os.ReadFile(*forgesFile)
	if err != nil {
		panic(err)
	}

	var forgesList []ForgeConfig
	if err := yaml.Unmarshal(forgesData, &forgesList); err != nil {
		panic(err)
	}

	forges := map[string]ForgeConfig{}
	for _, f := range forgesList {
		forges[f.Domain] = f
	}

	log.Info("Reading secrets configuration", "file", *secretsFile)

	secretsData, err := os.ReadFile(*secretsFile)
	if err != nil {
		panic(err)
	}

	var secretsList []ForgeSecret
	if err := yaml.Unmarshal(secretsData, &secretsList); err != nil {
		panic(err)
	}

	secrets := map[string]ForgeSecret{}
	for _, s := range secretsList {
		secrets[s.Domain] = s
	}

	githubClients := map[string]*github.Client{}
	forgejoClients := map[string]*forgejo.Client{}
	for domain, forge := range forges {
		switch forge.Type {
		case ForgeTypeGitHub:
			var httpClient *http.Client
			if secret, ok := secrets[domain]; ok && secret.Token != "" {
				httpClient = oauth2.NewClient(
					ctx,
					oauth2.StaticTokenSource(
						&oauth2.Token{
							AccessToken: secret.Token,
						},
					),
				)
			}

			client := github.NewClient(httpClient)
			client.BaseURL, err = url.Parse(forge.API)
			if err != nil {
				panic(err)
			}

			githubClients[domain] = client
		case ForgeTypeForgejo:
			options := []forgejo.ClientOption{
				forgejo.SetContext(ctx),
			}
			if secret, ok := secrets[domain]; ok && secret.Token != "" {
				options = append(options, forgejo.SetToken(secret.Token))
			}

			client, err := forgejo.NewClient(forge.API, options...)
			if err != nil {
				panic(err)
			}

			forgejoClients[domain] = client
		}

		log.Debug("Initialized client for forge", "domain", domain, "api", forge.API, "type", forge.Type)
	}

	log.Info("Reading projects file", "file", *projectsFile)

	input, err := os.ReadFile(*projectsFile)
	if err != nil {
		panic(err)
	}

	var parsedInput []InputCategory
	if err := yaml.Unmarshal(input, &parsedInput); err != nil {
		panic(err)
	}

	parsedOutput := []OutputCategory{}
	for _, inputCategory := range parsedInput {
		log.Info("Processing category", "title", inputCategory.Title)

		outputCategory := OutputCategory{
			Title:    inputCategory.Title,
			Projects: []OutputProject{},
		}

		for _, inputProject := range inputCategory.Projects {
			parts := strings.SplitN(inputProject.Repo, "/", 3)
			if len(parts) != 3 {
				panic(fmt.Errorf("invalid repo format, expected domain/owner/repo: %s", inputProject.Repo))
			}

			domain := parts[0]
			owner := parts[1]
			repo := parts[2]

			forge, ok := forges[domain]
			if !ok {
				panic(fmt.Errorf("unknown forge domain: %s", domain))
			}

			log.Debug("Fetching repository", "domain", domain, "owner", owner, "repo", repo)

			var outputProject OutputProject
			switch forge.Type {
			case ForgeTypeGitHub:
				client, ok := githubClients[domain]
				if !ok {
					panic(fmt.Errorf("no GitHub client for forge domain: %s", domain))
				}

				project, _, err := client.Repositories.Get(ctx, owner, repo)
				if err != nil {
					panic(err)
				}

				license := "UNLICENSED"
				if l := project.GetLicense(); l != nil {
					license = l.GetSPDXID()
				}

				commits, _, err := client.Repositories.ListCommits(ctx, owner, repo, &github.CommitsListOptions{})
				if err != nil {
					panic(err)
				}

				latestCommitDate := project.GetPushedAt().Time
				if len(commits) > 0 {
					latestCommitDate = commits[0].Commit.Author.Date.Time
				}

				icon := ""
				if inputProject.Icon != "" {
					icon = forge.CDN + owner + "/" + repo + "/" + project.GetDefaultBranch() + "/" + inputProject.Icon
				}

				outputProject = OutputProject{
					URL:         project.GetHTMLURL(),
					Title:       project.GetFullName(),
					Description: project.GetDescription(),
					Language:    project.GetLanguage(),
					License:     license,
					Date:        latestCommitDate.Format(time.RFC3339),
					Topics:      project.Topics,
					Stars:       project.GetStargazersCount(),
					Forks:       project.GetForksCount(),
					Issues:      project.GetOpenIssuesCount(),
					Icon:        icon,
					ForgeDomain: domain,
					ForgeEmoji:  forge.Emoji,
				}
			case ForgeTypeForgejo:
				client, ok := forgejoClients[domain]
				if !ok {
					panic(fmt.Errorf("no Forgejo client for forge domain: %s", domain))
				}

				project, _, err := client.GetRepo(owner, repo)
				if err != nil {
					panic(err)
				}

				commits, _, err := client.ListRepoCommits(owner, repo, forgejo.ListCommitOptions{})
				if err != nil {
					panic(err)
				}

				languages, _, err := client.GetRepoLanguages(owner, repo)
				if err != nil {
					panic(err)
				}

				primaryLanguage := ""
				var maxBytes int64
				for lang, bytes := range languages {
					if bytes > maxBytes {
						maxBytes = bytes
						primaryLanguage = lang
					}
				}

				latestCommitDate := project.Updated
				if len(commits) > 0 {
					if commitDate, err := time.Parse(time.RFC3339, commits[0].RepoCommit.Author.Date); err == nil {
						latestCommitDate = commitDate
					}
				}

				icon := ""
				if inputProject.Icon != "" {
					icon = forge.CDN + owner + "/" + repo + "/raw/branch/" + project.DefaultBranch + "/" + inputProject.Icon
				}

				outputProject = OutputProject{
					URL:         project.HTMLURL,
					Title:       project.FullName,
					Description: project.Description,
					Language:    primaryLanguage,
					License:     "",
					Date:        latestCommitDate.Format(time.RFC3339),
					Topics:      []string{},
					Stars:       project.Stars,
					Forks:       project.Forks,
					Issues:      project.OpenIssues,
					Icon:        icon,
					ForgeDomain: domain,
					ForgeEmoji:  forge.Emoji,
				}
			}

			outputCategory.Projects = append(outputCategory.Projects, outputProject)

			log.Debug("Processed repository", "fullName", outputProject.Title, "stars", outputProject.Stars)
		}

		parsedOutput = append(parsedOutput, outputCategory)
	}

	markdownOutput := ""
	for _, outputCategory := range parsedOutput {
		sort.Slice(outputCategory.Projects, func(i, j int) bool {
			return outputCategory.Projects[i].Stars > outputCategory.Projects[j].Stars
		})

		markdownOutput += "\n| **" + html.EscapeString(outputCategory.Title) + "** | |\n| - | - |\n"

		for i := 0; i < len(outputCategory.Projects); {
			markdownLine := "| "
			for j := 0; j < 2 && i+j < len(outputCategory.Projects); j++ {
				project := outputCategory.Projects[i+j]

				parsedDate, err := time.Parse(time.RFC3339, project.Date)
				if err != nil {
					panic(err)
				}
				formattedDate := parsedDate.Format("2006")

				iconMarkdown := ""
				if project.Icon != "" {
					iconMarkdown = fmt.Sprintf("<img alt=\"Icon\" src=\"%s\" height=\"24\" align=\"top\"> ", html.EscapeString(project.Icon))
				}

				titleParts := strings.Split(project.Title, "/")
				displayedTitle := project.Title
				if len(titleParts) == 2 {
					if titleParts[0] == *user {
						displayedTitle = titleParts[1]
					} else {
						displayedTitle = path.Join(titleParts[0], titleParts[1])
					}
				}

				displayedTitle = project.ForgeEmoji + "/" + displayedTitle

				languagePart := ""
				if project.Language != "" {
					languagePart = fmt.Sprintf(" 🛠️ %s", html.EscapeString(project.Language))
				}

				licensePart := ""
				if project.License != "" {
					licensePart = fmt.Sprintf(" ⚖️ %s", html.EscapeString(project.License))
				}

				projectMarkdown := fmt.Sprintf("<a display=\"inline\" target=\"_blank\" href=\"%s\"><b>%s%s</b></a> (⭐ %d%s%s 📅 %s) <br>%s",
					html.EscapeString(project.URL),
					iconMarkdown,
					html.EscapeString(displayedTitle),
					project.Stars,
					languagePart,
					licensePart,
					formattedDate,
					html.EscapeString(project.Description),
				)
				markdownLine += projectMarkdown + " | "
			}
			markdownOutput += markdownLine + "\n"
			i += 2
		}
	}

	fmt.Print(markdownOutput)
}
