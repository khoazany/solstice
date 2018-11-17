package srcmap

import (
	"errors"
	"encoding/json"
	"html"
	"io"
	"os"
	"os/exec"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/coordination-institute/debugging-tools/common"
)

type OpSourceLocation struct {
	ByteOffset     int
	ByteLength     int
	SourceFileName string
	JumpType       rune
}

type solcCombinedJSON struct {
	Contracts  map[string]runtimeArtifacts
	SourceList []string
	Sources    map[string]topASTNode
}

type runtimeArtifacts struct {
	SrcmapRuntime string `json:"srcmap-runtime"`
	BinRuntime    string `json:"bin-runtime"`
}

func Get(contractsPath string) (map[string][]OpSourceLocation, map[string]string, error) {
	var srcMapJSON solcCombinedJSON

	var files []string
	err := filepath.Walk(contractsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".sol") {
			files = append(files, path)
		}
		return nil
	})

	if err != nil {
		return map[string][]OpSourceLocation{}, map[string]string{}, err
	}

	solcArgs := append(
		[]string{
			"openzeppelin-solidity=./vendor/openzeppelin-solidity",
			"--optimize",
			"--combined-json=srcmap-runtime,bin-runtime",
		},
		files...)

	cmd := exec.Command("solc", solcArgs...)
	cmd.Dir = contractsPath
	out, err := cmd.Output()
	if err != nil {
		return map[string][]OpSourceLocation{}, map[string]string{}, err
	}

	err = json.Unmarshal(out, &srcMapJSON)
	if err != nil {
		return map[string][]OpSourceLocation{}, map[string]string{}, err
	}

	bytecodeToFilename := make(map[string]string)
	for contractName, artifacts := range srcMapJSON.Contracts {
		if len(artifacts.BinRuntime) != 0 {
			bytecode := "0x" + artifacts.BinRuntime
			bytecodeToFilename[common.RemoveMetaData(bytecode)] = contractName
		}
	}

	sourceMaps := map[string][]OpSourceLocation{}
	for contractName, artifacts := range srcMapJSON.Contracts {
		srcMapSlice := strings.Split(artifacts.SrcmapRuntime, ";")

		var opSourceLocations []OpSourceLocation
		for i, instructionTuple := range srcMapSlice {
			var currentStruct OpSourceLocation
			if i == 0 {
				currentStruct = OpSourceLocation{}
			} else {
				currentStruct = opSourceLocations[len(opSourceLocations)-1]
			}
			for j, val := range strings.Split(instructionTuple, ":") {
				// We do this because the split tuple might have any length <= 4.
				// Most of these cases won't be hit for most tuples.
				if val != "" {
					var err error
					if j == 0 {
						currentStruct.ByteOffset, err = strconv.Atoi(val)
					} else if j == 1 {
						currentStruct.ByteLength, err = strconv.Atoi(val)
					} else if j == 2 {
						sourceFileIndex, err := strconv.Atoi(val)
						if err != nil {
							return sourceMaps, bytecodeToFilename, err
						}
						if sourceFileIndex != -1 {
							currentStruct.SourceFileName = srcMapJSON.SourceList[sourceFileIndex]
						} else {
							currentStruct.SourceFileName = ""
						}
					} else if j == 3 {
						currentStruct.JumpType = rune(val[0])
					}
					if err != nil {
						return sourceMaps, bytecodeToFilename, err
					}
				}
			}
			opSourceLocations = append(opSourceLocations, currentStruct)
		}
		sourceMaps[contractName] = opSourceLocations
	}
	return sourceMaps, bytecodeToFilename, err
}


type topASTNode struct {
	AST JSONASTTree
}

type JSONASTTree struct {
	Id int
	Src string
	Children []*JSONASTTree
	// name string
	// attributes, which is a rich collection of information we're not using
}

type ASTTree struct {
	Id int
	SrcLoc OpSourceLocation
	Children []*ASTTree
}

func GetAST(contractName string, contractsPath string) (ASTTree, error) {
	cmd := exec.Command(
		"solc",
		"openzeppelin-solidity=./vendor/openzeppelin-solidity",
		"--optimize",
		"--combined-json=ast",
		contractName,
	)
	cmd.Dir = contractsPath
	out, err := cmd.Output()
	if err != nil {
		return ASTTree{}, err
	}

	var srcMapJSON solcCombinedJSON
	err = json.Unmarshal(out, &srcMapJSON)
	if err != nil {
		return ASTTree{}, err
	}

	processedTree, err := processASTNode(
		srcMapJSON.Sources[contractName].AST,
		srcMapJSON.SourceList,
		contractsPath,
	)
	if err != nil {
		return processedTree, err
	}

	return processedTree, nil
}


func processASTNode(node JSONASTTree, sourceList []string, contractsPath string) (ASTTree, error) {
	var newTree ASTTree

	srcLocParts := strings.Split(node.Src, ":")

	byteOffset, err := strconv.Atoi(srcLocParts[0])
	if err != nil {
		return newTree, err
	}

	byteLength, err := strconv.Atoi(srcLocParts[1])
	if err != nil {
		return newTree, err
	}

	sourceFileIndex, err := strconv.Atoi(srcLocParts[2])
	if err != nil {
		return newTree, err
	}

	newTree.Id = node.Id
	newTree.SrcLoc = OpSourceLocation{
		byteOffset,
		byteLength,
		contractsPath + "/" + sourceList[sourceFileIndex],
		*new(rune),
	}

	for _, childNode:= range node.Children {
		newNode, err := processASTNode(*childNode, sourceList, contractsPath)
		if err != nil {
			return newTree, err
		}
		newTree.Children = append(newTree.Children, &newNode)
	}

	return newTree, nil
}


func (location OpSourceLocation) ByteLocToSnippet() (int, int, []byte, error) {
	sourceFileReader, err := os.Open(location.SourceFileName)
	if err != nil {
		return 0, 0, []byte{}, err
	}
	sourceFileBeginning := make([]byte, location.ByteOffset+location.ByteLength)

	_, err = io.ReadFull(sourceFileReader, sourceFileBeginning)
	if err != nil {
		return 0, 0, []byte{}, err
	}
	defer sourceFileReader.Close()

	lineNumber := 1
	columnNumber := 1
	var codeSnippet []byte
	for byteIndex, sourceByte := range sourceFileBeginning {
		if byteIndex < location.ByteOffset {
			columnNumber += 1
			if sourceByte == '\n' {
				lineNumber += 1
				columnNumber = 1
			}
		} else {
			codeSnippet = append(codeSnippet, sourceByte)
		}
	}
	return lineNumber, columnNumber, codeSnippet, nil
}

const githubGreen string = "#e6ffed"

func (location OpSourceLocation) LocationMarkup() ([]byte, error) {
	if location.SourceFileName == "" {
		return []byte{}, errors.New("Step source file not found.")
	}

	wholeSrc, err := ioutil.ReadFile(location.SourceFileName)
	if err != nil {
		return []byte{}, err
	}

	srcBeginning := html.EscapeString(string(wholeSrc[0:location.ByteOffset]))
	srcMiddle := html.EscapeString(string(wholeSrc[location.ByteOffset : location.ByteOffset+location.ByteLength]))
	srcEnd := html.EscapeString(string(wholeSrc[location.ByteOffset+location.ByteLength : len(wholeSrc)]))

	return []byte("<pre>" +
		srcBeginning +
		"<span style=\"background-color:" + githubGreen + ";\">" +
		srcMiddle +
		"</span>" +
		srcEnd +
		"</pre>",
	), nil
}
