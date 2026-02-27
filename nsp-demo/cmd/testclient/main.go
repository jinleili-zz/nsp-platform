// Package main provides a test client for AK/SK authentication.
package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-resty/resty/v2"
	"github.com/yourorg/nsp-common/pkg/auth"
)

func main() {
	signer := auth.NewSigner("test-ak", "test-sk-1234567890abcdef")

	fmt.Println("========================================")
	fmt.Println("===  基于 net/http 的测试")
	fmt.Println("========================================")

	fmt.Println("\n=== 测试 1: GET /hello?name=Test ===")
	testGET(signer)

	fmt.Println("\n=== 测试 2: GET /user?id=123 ===")
	testUser(signer)

	fmt.Println("\n=== 测试 3: POST 请求带 Body ===")
	testPOST(signer)

	fmt.Println("\n=== 测试 4: 错误的 SK（应返回 401）===")
	testWrongSK()

	fmt.Println("\n=== 测试 5: 不存在的 AK（应返回 401）===")
	testWrongAK()

	fmt.Println("\n\n========================================")
	fmt.Println("===  基于 go-resty 的测试")
	fmt.Println("========================================")

	fmt.Println("\n=== Resty 测试 1: GET /hello?name=Test ===")
	testRestyGET(signer)

	fmt.Println("\n=== Resty 测试 2: GET /user?id=123 ===")
	testRestyUser(signer)

	fmt.Println("\n=== Resty 测试 3: POST 请求带 Body ===")
	testRestyPOST(signer)

	fmt.Println("\n=== Resty 测试 4: 错误的 SK（应返回 401）===")
	testRestyWrongSK()

	fmt.Println("\n=== Resty 测试 5: 不存在的 AK（应返回 401）===")
	testRestyWrongAK()
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

// ========================================
// go-resty 版本的测试函数
// ========================================

// restyPreRequestHook 是 go-resty 的 PreRequestHook，用于 AK/SK 签名
// 使用 PreRequestHook 而不是 RequestMiddleware，因为需要在请求完全构造好之后再签名
func restyPreRequestHook(signer *auth.Signer) func(*resty.Client, *http.Request) error {
	return func(c *resty.Client, req *http.Request) error {
		// 直接对标准 http.Request 进行签名
		if err := signer.Sign(req); err != nil {
			return fmt.Errorf("failed to sign request: %w", err)
		}
		return nil
	}
}

func testRestyGET(signer *auth.Signer) {
	client := resty.New()
	client.SetPreRequestHook(restyPreRequestHook(signer))

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetQueryParam("name", "Test").
		Get("http://localhost:8080/hello")

	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}

	fmt.Printf("状态码: %d\n响应: %s\n", resp.StatusCode(), resp.String())
}

func testRestyUser(signer *auth.Signer) {
	client := resty.New()
	client.SetPreRequestHook(restyPreRequestHook(signer))

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetQueryParam("id", "123").
		Get("http://localhost:8080/user")

	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}

	fmt.Printf("状态码: %d\n响应: %s\n", resp.StatusCode(), resp.String())
}

func testRestyPOST(signer *auth.Signer) {
	client := resty.New()
	client.SetPreRequestHook(restyPreRequestHook(signer))

	payload := `{"name":"张三","age":25}`
	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody([]byte(payload)).
		Post("http://localhost:8080/hello")

	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}

	fmt.Printf("状态码: %d\n响应: %s\n", resp.StatusCode(), resp.String())
}

func testRestyWrongSK() {
	wrongSigner := auth.NewSigner("test-ak", "wrong-secret-key")
	client := resty.New()
	client.SetPreRequestHook(restyPreRequestHook(wrongSigner))

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		Get("http://localhost:8080/hello")

	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}

	fmt.Printf("状态码: %d\n响应: %s\n", resp.StatusCode(), resp.String())
}

func testRestyWrongAK() {
	wrongSigner := auth.NewSigner("nonexistent-ak", "some-secret-key")
	client := resty.New()
	client.SetPreRequestHook(restyPreRequestHook(wrongSigner))

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		Get("http://localhost:8080/hello")

	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}

	fmt.Printf("状态码: %d\n响应: %s\n", resp.StatusCode(), resp.String())
}
