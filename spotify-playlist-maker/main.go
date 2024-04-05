package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tkanos/gonfig"
	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
	"golang.org/x/exp/maps"
	"golang.org/x/oauth2"
)

const (
	authConfigFileDefaultPath = "/conf/config.json"
	redirectURI               = "http://localhost:8080/callback"
	maxConcurrentUpdates      = 5
	playlistNameConvention    = `[0-9]{4}\.[0-9]{2}`
	YYYYMM                    = "YYYY.MM"
	getItemLimit              = 20
	defaultSearchPeriod       = "6"
	defaultRemoveUnlikedSongs = "true"
	defaultRunInterval        = "15"
)

var (
	auth = spotifyauth.New(
		spotifyauth.WithRedirectURL(redirectURI),
		spotifyauth.WithScopes(spotifyauth.ScopeUserLibraryRead, spotifyauth.ScopeUserLibraryModify, spotifyauth.ScopePlaylistModifyPublic),
	)
	ch    = make(chan Auth)
	state = "spotifyPlaylistMaker"
	limit = spotify.Limit(getItemLimit)
)

type Auth struct {
	RefreshToken string
}

func checkErr(e error) {
	if e != nil {
		log.Fatal(e)
	}
}

func printHelp() {
	fmt.Println("Help")
}

func authenticate() Auth {
	var ret Auth
	http.HandleFunc("/callback", completeAuth)
	go func() {
		http.ListenAndServe(":8080", nil)
	}()
	url := auth.AuthURL(state)
	fmt.Printf("Please log in to Spotify using this url:\n%s\n\n", url)
	ret = <-ch

	return ret
}

func completeAuth(w http.ResponseWriter, r *http.Request) {
	tok, err := auth.Token(r.Context(), state, r)
	if err != nil {
		http.Error(w, "Couldn't get token", http.StatusForbidden)
		log.Fatal(err)
	}

	user_auth := Auth{
		RefreshToken: tok.RefreshToken,
	}

	ch <- user_auth
}

func writeAuthToFile(authFilePath string, auth Auth) {
	file, _ := json.MarshalIndent(auth, "", " ")
	_ = os.WriteFile(authFilePath, file, 0644)
	fmt.Printf("Config written to %s\n", authFilePath)
}

func processArgs(args []string) {
	run_auth := false
	auth_to_file := false

	for _, arg := range args {
		switch arg {
		case "--auth":
			run_auth = true
		case "--file":
			auth_to_file = true
		case "--help":
			printHelp()
		default:
			fmt.Printf("Unknown argument: %s\n", arg)
		}
	}

	if run_auth {
		auth := authenticate()
		json_auth, _ := json.Marshal(auth)

		if auth_to_file {
			writeAuthToFile(authConfigFileDefaultPath, auth)
		} else {
			fmt.Printf("Paste the following into the config file or set as REFRESH_TOKEN ENV var:\n%s\n", json_auth)
		}
	}
}

func initClient(c context.Context, configFile string) (*spotify.Client, string) {
	var client *spotify.Client
	var authLoc string
	authConfig := Auth{}

	// Prioritise env var over config file
	if val, found := os.LookupEnv("REFRESH_TOKEN"); found {
		authConfig.RefreshToken = val
		authLoc = "Environment variable"
	} else {
		err := gonfig.GetConf(configFile, &authConfig)
		checkErr(err)
		authLoc = "Config file"
	}

	token := &oauth2.Token{
		RefreshToken: authConfig.RefreshToken,
		Expiry:       time.Now().Add(-time.Hour),
	}
	client = spotify.New(auth.Client(c, token))

	return client, authLoc
}

func setVar(e string, d string) string {
	val, ok := os.LookupEnv(e)
	if ok {
		return val
	} else {
		return d
	}
}

func playlistMappingContains(s map[string]spotify.ID, str string) bool {
	for k := range s {
		if k == str {
			return true
		}
	}

	return false
}

func updatePlaylist(c *spotify.Client, ctx context.Context, plMapping map[string]spotify.ID, playlistName string, likedSongs map[spotify.ID]struct{}, removeUnlikedSongs bool, wg *sync.WaitGroup) {
	playlistID := plMapping[playlistName]

	// Get existing songs in playlist
	plSongs, err := c.GetPlaylistItems(ctx, playlistID)
	checkErr(err)
	totalPlSongs := plSongs.Total
	plSongIds := map[spotify.ID]struct{}{}
	for i := 0; i <= totalPlSongs; i += getItemLimit {
		chunkedPlSongs, err := c.GetPlaylistItems(ctx, playlistID, spotify.Offset(i), spotify.Limit(getItemLimit))
		checkErr(err)
		for _, s := range chunkedPlSongs.Items {
			plSongIds[s.Track.Track.ID] = struct{}{}
		}
	}

	// Compare for new or deleted songs

	var unWantedSongIds []spotify.ID
	for id := range plSongIds {
		if _, found := likedSongs[id]; found {
			// in playlist already and liked so do nothing with it
			delete(likedSongs, id)
		} else {
			// in playlist but lo longer liked so add it to remove list
			unWantedSongIds = append(unWantedSongIds, id)
		}
	}

	// Add the new songs to the playlist

	// Create a slice out of the map
	wantedSongIds := maps.Keys(likedSongs)
	fmt.Printf("Playlist %s: Added: %d track(s) Removed: %d track(s)\n", playlistName, len(wantedSongIds), len(unWantedSongIds))

	songChunkSize := 100
	// add songs to playlist in batches of 100
	for i := 0; i < len(wantedSongIds); i += songChunkSize {
		end := i + songChunkSize

		// necessary check to avoid slicing beyond slice capacity
		if end > len(wantedSongIds) {
			end = len(wantedSongIds)
		}

		trackIds := wantedSongIds[i:end]

		_, err := c.AddTracksToPlaylist(ctx, playlistID, trackIds...)
		checkErr(err)

	}

	// Remove the unliked songs from the playlist
	if removeUnlikedSongs {
		for i := 0; i < len(unWantedSongIds); i += songChunkSize {
			end := i + songChunkSize

			// necessary check to avoid slicing beyond slice capacity
			if end > len(unWantedSongIds) {
				end = len(unWantedSongIds)
			}

			trackIds := unWantedSongIds[i:end]

			_, err := c.RemoveTracksFromPlaylist(ctx, playlistID, trackIds...)
			checkErr(err)

		}
	}

	wg.Done()
}

func run(client *spotify.Client, ctx context.Context, user *spotify.PrivateUser, searchPeriod int, boolremovedUnlikedSongs bool, processAllTracks bool) {

	// Todays date
	today := time.Now()
	// Get YYYY.MM date of the furthest point back we want to search
	cutoffMonth := today.AddDate(0, -(searchPeriod), 0)
	cutoffMonthFormatted := cutoffMonth.Format("2006.01")

	//
	// Get tracks
	//

	playlistSongs := map[string]map[spotify.ID]struct{}{}
	// Regex for AddedAt to get only YYYY-MM
	dateAddedre := regexp.MustCompile(`^[0-9]{4}-[0-9]{2}`)

	tracks, err := client.CurrentUsersTracks(ctx)
	total := tracks.Total
	checkErr(err)
	breakLoop := false
	// Create our map of playlist name to song Ids
	for i := 0; i <= total; i = i + getItemLimit {
		tracks, err := client.CurrentUsersTracks(ctx, spotify.Offset(i), limit)
		checkErr(err)

		for _, element := range tracks.Tracks {
			dateAdded := dateAddedre.FindString(element.AddedAt)
			dateAddedDotFormat := strings.Replace(dateAdded, "-", ".", 1)
			checkErr(err)

			// if we're not processing all tracks then check if this track goes beyond our search timeframe - break if it does.
			if !processAllTracks {
				if dateAddedDotFormat < cutoffMonthFormatted {
					breakLoop = true
					break
				}
			}

			id := element.ID
			if _, found := playlistSongs[dateAddedDotFormat]; found {
				playlistSongs[dateAddedDotFormat][id] = struct{}{}
			} else {
				playlistSongs[dateAddedDotFormat] = map[spotify.ID]struct{}{}
				playlistSongs[dateAddedDotFormat][id] = struct{}{}
			}
		}

		// we've found all the tracks we want, break now
		if breakLoop {
			break
		}
	}

	//
	// Get playlists
	//

	// Get all playlists
	p, err := client.CurrentUsersPlaylists(ctx)
	checkErr(err)

	// Playlist name format YYYY.MM
	re := regexp.MustCompile(playlistNameConvention)

	// Map of playlist names to ids
	playlistMapping := map[string]spotify.ID{}

	for i := 0; i <= p.Total; i = i + getItemLimit {
		playlists, err := client.CurrentUsersPlaylists(ctx, spotify.Offset(i), limit)
		checkErr(err)
		for _, pl := range playlists.Playlists {
			// Check if playlist name matches what we want
			if re.MatchString(pl.Name) {
				playlistMapping[pl.Name] = pl.ID
			}
		}
	}

	//
	// Create/Update playlists
	//

	sem := make(chan int, maxConcurrentUpdates)
	var wg sync.WaitGroup
	for pName, songs := range playlistSongs {

		// Make sure playlist exists before updating playlists otherwise a duplicate playlist can be made
		if !playlistMappingContains(playlistMapping, pName) {
			p, err := client.CreatePlaylistForUser(ctx, user.ID, pName, "", true, false)
			checkErr(err)
			playlistMapping[pName] = p.ID
		}

		sem <- 1 // will block if there is MAX ints in sem
		wg.Add(1)
		go func(playlistName string, songList map[spotify.ID]struct{}) {
			updatePlaylist(client, context.Background(), playlistMapping, playlistName, songList, boolremovedUnlikedSongs, &wg)
			<-sem // removes an int from sem, allowing another to proceed
		}(pName, songs)
	}

	wg.Wait()
	fmt.Println("Run finished.")
}

func main() {
	// Process args
	argsWithoutProg := os.Args[1:]
	processArgs(argsWithoutProg)

	//
	// set overriding vars
	//

	// default search period, converted to int from string
	searchPeriod, _ := strconv.Atoi(setVar("SEARCH_PERIOD", defaultSearchPeriod))

	// remove songs if unliked
	removedUnlikedSongs := setVar("REMOVE_UNLIKED_SONGS", defaultRemoveUnlikedSongs)
	boolremovedUnlikedSongs, _ := strconv.ParseBool(removedUnlikedSongs)

	// Run interval
	runInterval, _ := strconv.Atoi(setVar("RUN_INTERVAL", defaultRunInterval))

	// config file
	authConfigFile := setVar("CONFIG_FILE_PATH", authConfigFileDefaultPath)

	//
	// init client
	//

	// Get our client to be used for operations
	ctx := context.Background()
	client, authLoc := initClient(ctx, authConfigFile)

	user, err := client.CurrentUser(ctx)
	checkErr(err)

	//
	// Configure search period
	//

	// Remove 1 from searchPeriod so we get the correct number of months to search back
	var realSearchPeriod int
	var processAllTracks bool
	if searchPeriod == 0 {
		processAllTracks = true
	} else {
		realSearchPeriod = searchPeriod - 1
	}

	//
	// Print config to console
	//

	printedConfig := `Using config:
	Username: %s
	Auth type: %s
	Search period: %s
	Remove unliked songs: %s
	Run Interval: %s mins
	
`
	fmt.Printf(printedConfig, user.ID, authLoc, strconv.Itoa(searchPeriod), removedUnlikedSongs, strconv.Itoa(runInterval))

	//
	// Infinite run loop
	//

	for {
		run(client, ctx, user, realSearchPeriod, boolremovedUnlikedSongs, processAllTracks)
		time.Sleep(time.Second * 60 * time.Duration(runInterval))
	}

}
