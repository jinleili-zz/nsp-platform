// Package main provides a test client for AK/SK authentication.
package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yourorg/nsp-common/pkg/auth"
)

func main() {
	signer := auth.NewSigner("test-ak", "test-sk-1234567890abcdef")

	fmt.Println("=== 测试 1: GET /hello?name=Test ===")
	testGET(signer)

	fmt.Println("\n=== 测试 2: GET /user?id=123 ===")
	testUser(signer)

	fmt.Println("\n=== 测试 3: POST 请求带 Body ===")
	testPOST(signer)

	fmt.Println("\n=== 测试 4: 错误的 SK（应返回 401）===")
	testWrongSK()

	fmt.Println("\n=== 测试 5: 不存在的 AK（应返回 401）===")
	testWrongAK()
}

func testGET(signer *auth.Signer) {
	req, _ := http.NewRequest("GET", "http://localhost:8080/hello?name=Test", nil)
	if err := signer.Sign(req); err != nil {
		fmt.Printf("签名失败: %v\n", err)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("状态码: %d\n响应: %s\n", resp.StatusCode, string(body))
}

func testUser(signer *auth.Signer) {
	req, _ := http.NewRequest("GET", "http://localhost:8080/user?id=123", nil)
	if err := signer.Sign(req); err != nil {
		fmt.Printf("签名失败: %v\n", err)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("状态码: %d\n响应: %s\n", resp.StatusCode, string(body))
}

func testPOST(signer *auth.Signer) {
	payload := `{"name":"张三","age":25}`
	req, _ := http.NewRequest("POST", "http://localhost:8080/hello", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	if err := signer.Sign(req); err != nil {
		fmt.Printf("签名失败: %v\n", err)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("状态码: %d\n响应: %s\n", resp.StatusCode, string(body))
}

func testWrongSK() {
	wrongSigner := auth.NewSigner("test-ak", "wrong-secret-key")
	req, _ := http.NewRequest("GET", "http://localhost:8080/hello", nil)
	if err := wrongSigner.Sign(req); err != nil {
		fmt.Printf("签名失败: %v\n", err)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("状态码: %d\n响应: %s\n", resp.StatusCode, string(body))
}

func testWrongAK() {
	wrongSigner := auth.NewSigner("nonexistent-ak", "some-secret-key")
	req, _ := http.NewRequest("GET", "http://localhost:8080/hello", nil)
	if err := wrongSigner.Sign(req); err != nil {
		fmt.Printf("签名失败: %v\n", err)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("状态码: %d\n响应: %s\n", resp.StatusCode, string(body))
}
