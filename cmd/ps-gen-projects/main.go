package main

import (
	"context"
	"flag"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/go-github/v42/github"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v2"
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
	Background  string   `yaml:"background"`
	Icon        string   `yaml:"icon"`
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
	Slug       string `yaml:"slug"`
	Background string `yaml:"background"`
	Icon       bool   `yaml:"icon"`
}

func main() {
	src := flag.String("src", "projects.yaml", "Source YAML file")
	api := flag.String("api", "https://api.github.com/", "GitHub/Gitea API endpoint to use (can also be set using the GITHUB_API env variable)")
	cdn := flag.String("cdn", "https://raw.githubusercontent.com/", "GitHub/Gitea CDN endpoint to use (can also be set using the GITHUB_CDN env variable)")
	token := flag.String("token", "", "GitHub/Gitea API access token (can also be set using the GITHUB_TOKEN env variable)")
	user := flag.String("user", "pojntfx", "GitHub username (can also be set using the GITHUB_USER env variable)")

	flag.Parse()

	if *api == "" {
		*api = os.Getenv("GITHUB_API")
	}

	if *cdn == "" {
		*cdn = os.Getenv("GITHUB_CDN")
	}

	if *token == "" {
		*token = os.Getenv("GITHUB_TOKEN")
	}

	if *user == "" {
		*user = os.Getenv("GITHUB_USER")
	}

	input, err := os.ReadFile(*src)
	if err != nil {
		panic(err)
	}

	var parsedInput []InputCategory
	if err := yaml.Unmarshal(input, &parsedInput); err != nil {
		panic(err)
	}

	var httpClient *http.Client
	if *token != "" {
		httpClient = oauth2.NewClient(
			context.Background(),
			oauth2.StaticTokenSource(
				&oauth2.Token{
					AccessToken: *token,
				},
			),
		)
	}

	client := github.NewClient(httpClient)
	client.BaseURL, err = url.Parse(*api)
	if err != nil {
		panic(err)
	}

	parsedOutput := []OutputCategory{}
	for _, inputCategory := range parsedInput {
		outputCategory := OutputCategory{
			Title:    inputCategory.Title,
			Projects: []OutputProject{},
		}

		for _, inputProject := range inputCategory.Projects {
			owner, repo := path.Split(inputProject.Slug)

			project, _, err := client.Repositories.Get(context.Background(), strings.TrimSuffix(owner, "/"), repo)
			if err != nil {
				panic(err)
			}

			license := "UNLICENSED"
			if l := project.GetLicense(); l != nil {
				license = l.GetSPDXID()
			}

			commits, _, err := client.Repositories.ListCommits(context.Background(), strings.TrimSuffix(owner, "/"), repo, &github.CommitsListOptions{})
			if err != nil {
				panic(err)
			}

			latestCommitDate := project.GetPushedAt().Time
			if len(commits) > 0 {
				latestCommitDate = *commits[0].Commit.Author.Date
			}

			icon := ""
			if inputProject.Icon {
				icon = *cdn + owner + repo + "/" + project.GetDefaultBranch() + "/docs/icon-light.png"
			}

			outputCategory.Projects = append(outputCategory.Projects, OutputProject{
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
				Background:  inputProject.Background,
				Icon:        icon,
			})
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

				displayedTitle := project.Title
				if strings.Split(project.Title, "/")[0] == *user {
					displayedTitle = strings.Split(project.Title, "/")[1] // Use just the repo name if owner matches the GitHub user
				}

				projectMarkdown := fmt.Sprintf("<a display=\"inline\" target=\"_blank\" href=\"%s\"><b>%s%s</b></a> (⭐ %d 🛠️ %s ⚖️ %s 📅 %s) <br>%s",
					html.EscapeString(project.URL),
					iconMarkdown,
					html.EscapeString(displayedTitle),
					project.Stars,
					html.EscapeString(project.Language),
					html.EscapeString(project.License),
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
