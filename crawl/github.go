package crawl

import (
	"fmt"
	"log"
	"os"
	"regexp"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"

	"github.com/impact/impact/parsing"
	"github.com/impact/impact/recorder"
)

type GitHubCrawler struct {
	token   string
	pattern string
	re      *regexp.Regexp
	user    string
}

var exclusionList []string

func init() {
	exclusionList = []string{
		"modelica-3rdparty:BrineProp:0.1.9",  // Directory structure is a mess
		"modelica-3rdparty:ModelicaDEVS:1.0", // Self reference (and invalid at that)
		"modelica-3rdparty:NCLib:0.82",       // Missing package.mo
	}
}

func exclude(user string, reponame string, tagname string) bool {
	str := fmt.Sprintf("%s:%s:%s", user, reponame, tagname)
	for _, ex := range exclusionList {
		//log.Printf("Comparing '%s' to '%s'", ex, str)
		if ex == str {
			return true
		}
	}
	return false
}

func (c GitHubCrawler) processVersion(client *github.Client, r recorder.Recorder,
	altname string, repo github.Repository, versionString string, sha string, tarurl string,
	zipurl string, verbose bool, logger *log.Logger) {

	rname := *repo.Name

	v, verr := parsing.NormalizeVersion(versionString)
	if verr != nil {
		// If not, ignore it
		if verbose {
			logger.Printf("  %s: Ignoring", versionString)
		}
		return
	}

	if verbose {
		logger.Printf("  %s: Recording", versionString)
	}

	ownerid := *repo.Owner.Login
	// Formulate directory info (impact.json) for this version of this repository
	di := ExtractInfo(client, ownerid, altname, repo, sha, versionString, verbose, logger)

	if len(di.Libraries) == 0 {
		logger.Printf("    No Modelica libraries found in repository %s:%s",
			rname, versionString)
		return
	}

	// Loop over all libraries present in this repository
	for _, lib := range di.Libraries {
		if verbose {
			logger.Printf("    Processing library %s @ %s", lib.Name, lib.Path)
		}

		if repo.HTMLURL == nil {
			logger.Printf("Error: Cannot index because HTMLURL is not specified")
			continue
		}

		libr := r.GetLibrary(lib.Name, *repo.HTMLURL, di.OwnerURI)

		if repo.Description != nil {
			libr.SetDescription(*repo.Description)
		}

		libr.SetHomepage(*repo.HTMLURL)
		libr.SetRepository(*repo.GitURL, "git")
		libr.SetStars(*repo.StargazersCount)
		libr.SetEmail(di.Email)

		vr := libr.AddVersion(v)

		vr.SetPath(lib.Path, lib.IsFile)
		vr.SetHash(sha)
		vr.SetTarballURL(tarurl)
		vr.SetZipballURL(zipurl)

		for _, dep := range lib.Dependencies {
			vr.AddDependency(dep.Name, dep.Version)
		}
	}
}

func (c GitHubCrawler) Crawl(r recorder.Recorder, verbose bool, logger *log.Logger) error {
	// Start with whatever token we were given when this crawler was created
	token := c.token

	// If a token wasn't provided with the crawler, look for a token
	// as an environment variable
	if c.token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}

	// Create a client assuming no authentication
	client := github.NewClient(nil)

	// If we have a token, re-initialize the client with
	// authentication
	if token != "" {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		tc := oauth2.NewClient(oauth2.NoContext, ts)

		client = github.NewClient(tc)
	}

	lopts := github.RepositoryListOptions{}
	lopts.Page = 1
	lopts.PerPage = 10

	if verbose {
		logger.Printf("Fetching repositories for %s", c.user)
	}
	repos := []github.Repository{}
	for {
		// Get a list of all repositories associated with the specified
		// organization
		page, _, err := client.Repositories.List(c.user, &lopts)
		if err != nil {
			logger.Printf("Error listing repositories for %s: %v", c.user, err)
			return fmt.Errorf("Error listing repositories for %s: %v", c.user, err)
		}
		repos = append(repos, page...)
		if verbose {
			logger.Printf("  Fetching page %d, %d entries", lopts.Page, len(page))
		}

		if len(page) == 0 {
			break
		}
		lopts.Page = lopts.Page + 1
	}

	// Loop over all repos associated with the given owner
	for _, minrepo := range repos {
		rname := *minrepo.Name
		single, _, err := client.Repositories.Get(c.user, rname)
		if err != nil {
			logger.Printf("Unable to fetch complete details for repo %s/%s: %v",
				c.user, rname, err)
			continue
		}

		if !c.re.MatchString(rname) {
			if verbose {
				logger.Printf("Skipping: %s (%s), doesn't match pattern '%s'",
					rname, *minrepo.HTMLURL, c.pattern)
			}
			continue
		}

		if verbose {
			logger.Printf("Processing: %s (%s, fork=%v)",
				rname, *minrepo.HTMLURL, *minrepo.Fork)
		}

		repo := *single

		// If this is a fork, index the "real" repository
		if *minrepo.Fork && single.Source != nil {
			repo = *single.Source
			if verbose {
				log.Printf("Source for %s exists", *repo.Name)
			}
		} else {
			if verbose {
				log.Printf("No source for %s", *repo.Name)
			}
		}

		// TODO: Record both Source and fork?!?

		/*
			if orepo.Parent != nil {
				repo = *orepo.Parent
				log.Printf("Parent for %s exists", *repo.Name)
			} else {
				log.Printf("No parent for %s", *repo.Name)
			}
		*/

		// Get all the tags associated with this repository
		tags, _, err := client.Repositories.ListTags(c.user, rname, nil)
		if err != nil {
			logger.Printf("Error getting tags for repository %s/%s: %v",
				c.user, rname, err)
			continue
		}

		// Loop over the tags
		for _, tag := range tags {
			if verbose {
				log.Printf("Processing tag %s", *tag.Name)
			}
			// Check if this has a semantic version
			versionString := *tag.Name
			sha := *tag.Commit.SHA

			if versionString[0] == 'v' {
				versionString = versionString[1:]
			}

			tarurl := ""
			if tag.TarballURL != nil {
				tarurl = *tag.TarballURL
			}

			zipurl := ""
			if tag.ZipballURL != nil {
				zipurl = *tag.ZipballURL
			}

			// Check for version we know are not supported
			if exclude(c.user, rname, versionString) {
				continue
			}

			c.processVersion(client, r, rname, repo, versionString, sha, tarurl, zipurl,
				verbose, logger)
		}

		// TODO: Add HEAD of master to list?  But how?  What kind of semantic
		// version number should I associate with it?
	}
	return nil
}

func (c GitHubCrawler) String() string {
	return fmt.Sprintf("github://%s/%s", c.user, c.pattern)
}

func MakeGitHubCrawler(user string, pattern string, token string) (GitHubCrawler, error) {
	if pattern == "" {
		pattern = ".+"
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return GitHubCrawler{}, err
	}

	return GitHubCrawler{
		token:   token,
		pattern: pattern,
		re:      re,
		user:    user,
	}, nil
}

var _ Crawler = (*GitHubCrawler)(nil)
