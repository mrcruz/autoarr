package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

var m_downloadClientUrl string
var m_downloadClientAPIUrl string
var m_rootDownloadPath string
var m_config Config

func main() {
	jsonFile, err := os.Open("/config/input.json")
	if err != nil {
		panic(err)
	}
	defer jsonFile.Close()

	byteValue, _ := ioutil.ReadAll(jsonFile)

	json.Unmarshal([]byte(byteValue), &m_config)

	// get variables
	poolSize := m_config.PoolSize * 1000000 * 1000

	m_downloadClientAPIUrl = m_config.DownloadClientUrl + "/api/v2/"
	m_downloadClientUrl = m_downloadClientAPIUrl + "torrents/"

	// sanity check on env variables
	if m_config.RcloneRemote != "" {
		rcloneConfigPath := "/home/user/.config/rclone/rclone.conf"
		if _, err := os.Stat(rcloneConfigPath); os.IsNotExist(err) {
			panic("no rclone config found on " + rcloneConfigPath)
		}
	}

	if poolSize <= 0 {
		panic("Pool Size not defined")
	}

	// get data from download client
	m_rootDownloadPath = GetRootDownloadPath()
	allDownloads := GetDownloadList()

	// sort them by priority
	sort.SliceStable(allDownloads, func(i, j int) bool {
		if m_config.SortInvertOrder {
			return allDownloads[i].GetFloat(m_config.SortField) > allDownloads[j].GetFloat(m_config.SortField)
		} else {
			return allDownloads[i].GetFloat(m_config.SortField) < allDownloads[j].GetFloat(m_config.SortField)
		}
	})

	// get list of soon to be active and soon to be idle downloads, respecting pool size
	var downloadsToEnable []Download
	var downloadsToDisable []Download

	var activeDownloadSize float64
	for _, download := range allDownloads {

		newDownloadSize := download.totalSize

		if download.IsIgnored() {
			if m_config.ConsiderIgnoredInPoolSize {
				activeDownloadSize += newDownloadSize
			}

			continue
		}

		// check if the download is a canditate for removal
		removeDownload := false
		for _, condition := range m_config.RemoveConditions {

			if condition.Value == 0 {
				panic("Value not defined for remove condition with field: " + condition.Field)
			}

			satisfiesCondition := false

			if condition.Invert {
				satisfiesCondition = download.GetFloat(condition.Field) < condition.Value
			} else {
				satisfiesCondition = download.GetFloat(condition.Field) > condition.Value
			}

			if satisfiesCondition {
				removeDownload = true
				if m_config.RemoveConditionInclusive == true {
					break
				}
			} else {
				removeDownload = false
				if m_config.RemoveConditionInclusive == false {
					break
				}
			}
		}

		if removeDownload {
			Log("Removing download: " + download.name)
			download.Remove()
			continue
		}

		// enable torrent if fits on active pool, disable if it does not
		if activeDownloadSize+newDownloadSize < poolSize {
			downloadsToEnable = append(downloadsToEnable, download)
			activeDownloadSize += newDownloadSize
		} else {
			downloadsToDisable = append(downloadsToDisable, download)
		}
	}

	for _, download := range downloadsToDisable {

		// do nothing if download is already inactive
		if download.active == false {
			continue
		}

		Log("Stoping download: " + download.name)

		download.Pause()

		if m_config.UseStash {
			download.Stash()
		}
	}

	for _, download := range downloadsToEnable {

		if download.active == true {
			continue
		}

		Log("Starting download: " + download.name)

		if m_config.UseStash {
			download.Retrieve()
		}

		if m_config.RecheckOnResume {
			download.Recheck()
		}

		download.Resume()
		download.Reannounce()
	}
}

func (download Download) IsIgnored() bool {
	values := []string{download.name, download.tags, download.category}
	blockItems := []string{m_config.IgnoreByName, m_config.IgnoreByTag, m_config.IgnoreByCategory}
	allowItems := []string{m_config.AllowByName, m_config.AllowByTag, m_config.AllowByCategory}

	for i, _ := range blockItems {
		if blockItems[i] == "" {
			continue
		}

		if strings.Contains(values[i], blockItems[i]) {
			return true
		}

	}

	for i, _ := range allowItems {
		if allowItems[i] == "" {
			continue
		}

		if !strings.Contains(values[i], allowItems[i]) {
			return true
		}
	}

	return false
}

func (download Download) GetRelativePath() string {
	return strings.TrimLeft(strings.ReplaceAll(download.contentPath, m_rootDownloadPath, ""), "/")
}

func (download Download) GetIdlePath() string {
	idlePath := "/idle/"
	if m_config.RcloneRemote != "" {
		idlePath = m_config.RcloneRemote + ":/"
	}
	return idlePath + download.GetRelativePath()
}

func (download Download) GetActivePath() string {
	return "/active/" + download.GetRelativePath()
}

func Log(line string) {
	line = time.Now().Local().Format("2006-01-02 15:04:05") + ": " + line

	fmt.Println(line)

	f, err := os.OpenFile("/config/autoarr.log",
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)

	if err != nil {
		panic(err)
	}

	defer f.Close()
	if _, err := f.WriteString("\n" + line); err != nil {
		panic(err)
	}
}

func Rclone(command ...string) {
	cmd := exec.Command("rclone", command...)
	fmt.Println(cmd)
	if m_config.DoNotChangeFiles {
		return
	}
	cmd.Output()
}

func (download Download) Retrieve() {
	Rclone("copy", download.GetIdlePath(), download.GetActivePath())
}

func (download Download) Stash() {
	action := "move"
	if m_config.DoNotDestroyFiles {
		action = "copy"
	}
	Rclone(action, download.GetActivePath(), download.GetIdlePath())
}

func (download Download) Remove() {
	if m_config.DoNotDestroyFiles == false {
		fmt.Println("Not removing download " + download.name + " because DoNotDestroyFiles is true")
		return
	}
	Rclone("purge", download.GetIdlePath())
	Rclone("purge", download.GetActivePath())
	MakeDownloadClientRequest("delete?hashes=" + download.hash + "&deleteFiles=true")
}

func (download Download) Recheck() {
	MakeDownloadClientRequest("recheck?hashes=" + download.hash)
}

func (download Download) Reannounce() {
	MakeDownloadClientRequest("reannounce?hashes=" + download.hash)
}

func (download Download) Resume() {
	MakeDownloadClientRequest("resume?hashes=" + download.hash)
}

func (download Download) Pause() {
	MakeDownloadClientRequest("pause?hashes=" + download.hash)
}

func (d Download) GetFloat(field string) float64 {
	if d.raw[field] == nil {
		panic("Problem when trying to get field: " + field)
	}
	return d.raw[field].(float64)
}

func MakeDownloadClientRequest(request string) {
	if m_config.DoNotChangeDownloadClient {
		fmt.Println("Skiping get: " + m_downloadClientUrl + request)
		return
	}
	http.Get(m_downloadClientUrl + request)
}

func GetRootDownloadPath() string {
	torrentClientUrl := m_downloadClientAPIUrl + "app/defaultSavePath"
	resp, err := http.Get(torrentClientUrl)

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}

	return string(body)
}

func GetDownloadList() []Download {
	torrentClientUrl := m_downloadClientUrl + "info"
	resp, err := http.Get(torrentClientUrl)

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}

	if string(body) == "Forbidden" {
		panic("Forbidden: unable to access your download client")
	}

	var clientResponse []map[string]interface{}

	if err := json.Unmarshal(body, &clientResponse); err != nil {
		panic(err)
	}

	var downloads []Download
	for _, downloadMap := range clientResponse {

		paused := downloadMap["state"] == "pausedUP" || downloadMap["state"] == "missingFiles"
		active := downloadMap["state"] == "queuedUP" || downloadMap["state"] == "uploading" || downloadMap["state"] == "stalledUP"

		// only include downloads with either of these states
		if !active && !paused {
			continue
		}

		newDowload := Download{
			raw:         downloadMap,
			active:      active,
			category:    downloadMap["category"].(string),
			contentPath: downloadMap["content_path"].(string),
			hash:        downloadMap["hash"].(string),
			name:        downloadMap["name"].(string),
			tags:        downloadMap["tags"].(string),
			totalSize:   downloadMap["total_size"].(float64),
		}
		downloads = append(downloads, newDowload)
	}

	return downloads
}

type Download struct {
	raw         map[string]interface{}
	active      bool
	category    string
	contentPath string
	hash        string
	name        string
	tags        string
	totalSize   float64
}

type Config struct {
	AllowByCategory           string
	AllowByName               string
	AllowByTag                string
	ConsiderIgnoredInPoolSize bool
	DoNotChangeDownloadClient bool
	DoNotChangeFiles          bool
	DoNotDestroyFiles         bool
	DownloadClientUrl         string
	IgnoreByCategory          string
	IgnoreByName              string
	IgnoreByTag               string
	PoolSize                  float64
	RcloneRemote              string
	RecheckOnResume           bool
	RemoveConditionInclusive  bool
	RemoveConditions          []Condition
	SortField                 string
	SortInvertOrder           bool
	UseStash                  bool
}

type Condition struct {
	Field  string
	Invert bool
	Value  float64
}
