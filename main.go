package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
	"golang.org/x/oauth2"
)

const (
	port         = ":8888"
	htmlTemplate = `
<!DOCTYPE html>
<html lang="en">
    <head>
        <title>Login Successful</title>
        <meta charset="UTF-8">
    </head>
    <body>
        <h1>Login Successful</h1>
        <p>If this page isn't automatically closing you can close it now.</p>
        <script>
            window.close();
        </script>
    </body>
</html>
`
)

var (
	auth = spotifyauth.New(spotifyauth.WithRedirectURL(redirectURI), spotifyauth.WithScopes(
		spotifyauth.ScopeUserReadPrivate,
		spotifyauth.ScopePlaylistReadPrivate,
		spotifyauth.ScopePlaylistReadCollaborative,
		spotifyauth.ScopePlaylistModifyPrivate,
		spotifyauth.ScopePlaylistModifyPublic,
		spotifyauth.ScopeUserReadPlaybackState,
	))
	ch          = make(chan *spotify.Client)
	state       = "abc123"
	redirectURI = fmt.Sprintf("http://localhost%s/callback", port)
)

var data Data

type Data struct {
	Token        oauth2.Token `json:"token"`
	PlaylistName string       `json:"playlistName"`
}

// TODO: Improve error handling (don't panic everywhere)
func main() {
	ctx := context.Background()

	var hasToSetPlaylist bool
	var remove bool

	flag.BoolVar(&hasToSetPlaylist, "reset", false, "To reset the playlist chosen by the first startup")
	flag.BoolVar(&remove, "r", false, "To remove the current song from the playlist instead of adding it")
	flag.Parse()

	// Create data variable and defer to write it
	defer writeData()

	// Get the client, either from config file or from oauth
	go getClient(ctx)

	// wait for auth to complete
	client := <-ch

	// Update token
	newToken, err := client.Token()
	if err != nil {
		panic(err)
	}

	data.Token = *newToken

	// Get current user
	user, err := client.CurrentUser(ctx)
	if err != nil {
		panic(err)
	}

	// Get Playlists of user
	playlists := getAllPlaylistsForUser(ctx, user, client)
	playlistNames := getPlaylistNames(playlists)

	// Set Playlist if needs to
	if hasToSetPlaylist || data.PlaylistName == "" {
		data.PlaylistName = selectPlaylist(playlistNames)
	}

	// Get playlist from name
	playlist := getPlaylistFromName(data.PlaylistName, playlists)

	// Get the currently playing track ID
	currentlyPlaying, err := client.PlayerCurrentlyPlaying(ctx)
	if err != nil {
		panic(err)
	}

	if currentlyPlaying == nil || currentlyPlaying.Item == nil {
		fmt.Println("Currently not playing a song")

		return
	}

	trackID := currentlyPlaying.Item.ID

	_, err = client.RemoveTracksFromPlaylist(ctx, playlist.ID, trackID)
	if err != nil {
		panic(err)
	}

	if remove {
		return
	}

	// // Get tracks From Playlist
	// tracks := getPlaylistTracksFromPlaylist(ctx, client, playlist)
	//
	// // Only add to playlist if not already in it
	// isTrackAlreadyInPlaylist := isTrackIDInTracks(trackID, tracks)
	//
	// if isTrackAlreadyInPlaylist {
	// 	return
	// }

	// Add track
	_, err = client.AddTracksToPlaylist(ctx, playlist.ID, trackID)
	if err != nil {
		panic(err)
	}
}

func getAllPlaylistsForUser(ctx context.Context, user *spotify.PrivateUser, client *spotify.Client) []spotify.SimplePlaylist {
	playlistsPagable, err := client.GetPlaylistsForUser(ctx, user.ID)
	if err != nil {
		panic(err)
	}

	playlists := []spotify.SimplePlaylist{}

	for {
		playlists = append(playlists, playlistsPagable.Playlists...)

		if err := client.NextPage(ctx, playlistsPagable); errors.Is(err, spotify.ErrNoMorePages) {
			break
		} else if err != nil {
			panic(err)
		}
	}

	return playlists
}

func getPlaylistTracksFromPlaylist(ctx context.Context, client *spotify.Client, playlist spotify.SimplePlaylist) []spotify.PlaylistTrack {
	p, err := client.GetPlaylistTracks(ctx, playlist.ID)
	if err != nil {
		panic(err)
	}

	tracks := []spotify.PlaylistTrack{}

	for {
		tracks = append(tracks, p.Tracks...)

		if err := client.NextPage(ctx, p); errors.Is(err, spotify.ErrNoMorePages) {
			break
		} else if err != nil {
			panic(err)
		}
	}

	return tracks
}

func getPlaylistNames(playlists []spotify.SimplePlaylist) []string {
	playlistNames := make([]string, len(playlists))
	for i, p := range playlists {
		playlistNames[i] = p.Name
	}

	return playlistNames
}

func selectPlaylist(playlists []string) string {
	prompt := promptui.Select{
		Label: "Select Playlist",
		Items: playlists,
	}
	prompt.Searcher = func(in string, i int) bool {
		return strings.Contains(
			strings.ToLower(prompt.Items.([]string)[i]),
			strings.ToLower(in),
		)
	}
	prompt.StartInSearchMode = true

	_, res, err := prompt.Run()
	if err != nil {
		panic(err)
	}

	return res
}

func getPlaylistFromName(name string, playlists []spotify.SimplePlaylist) (playlist spotify.SimplePlaylist) {
	for _, p := range playlists {
		if p.Name == name {
			playlist = p
		}
	}

	return
}

func isTrackIDInTracks(trackID spotify.ID, tracks []spotify.PlaylistTrack) (alreadyInPlaylist bool) {
	for _, track := range tracks {
		if trackID == track.Track.ID {
			alreadyInPlaylist = true
		}
	}

	return
}

func writeData() {
	out, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}

	file, err := os.Create(configFile)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	_, err = file.Write(out)
	if err != nil {
		panic(err)
	}
}

func openbrowser(url string) {
	var err error

	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = errors.New("unsupported platform")
	}

	if err != nil {
		panic(err)
	}
}

func completeAuth(w http.ResponseWriter, r *http.Request) {
	tok, err := auth.Token(r.Context(), state, r)
	if err != nil {
		http.Error(w, "Couldn't get token", http.StatusForbidden)
		panic(err)
	}

	if st := r.FormValue("state"); st != state {
		http.NotFound(w, r)
		panic(fmt.Sprintf("State mismatch: %s != %s\n", st, state))
	}

	_, err = w.Write([]byte(htmlTemplate))
	if err != nil {
		panic(err)
	}

	// use the token to get an authenticated client
	client := spotify.New(auth.Client(r.Context(), tok))
	ch <- client
}

func getClient(ctx context.Context) {
	dirname, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}

	configFile := dirname + "/.config/ostentatious/config.json"
	content, err := os.ReadFile(configFile)
	if err != nil {
		fmt.Printf("No config file %v found, processing with initial setup\n", configFile)
		startserver()

		return
	}

	err = json.Unmarshal(content, &data)
	if err != nil {
		fmt.Printf("Could not unmarshal JSON in file %v, processing with initial setup\n", configFile)
		startserver()

		return
	}

	client := spotify.New(auth.Client(ctx, &data.Token))
	if client == nil {
		fmt.Printf("Could not create client from data in file %v, processing with initial setup\n", configFile)
		startserver()

		return
	}
	ch <- client
}

func startserver() {
	http.HandleFunc("/callback", completeAuth)
	go func() {
		err := http.ListenAndServe(port, nil)
		if err != nil {
			panic(err)
		}
	}()

	url := auth.AuthURL(state)
	openbrowser(url)
}
