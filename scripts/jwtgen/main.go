package main

import (
    "fmt"
    "os"

    "github.com/alvindingpeng/switchyard/internal/model"
    "github.com/alvindingpeng/switchyard/internal/server/auth"
)

func main() {
    // 管理员: klarns (id=1, role=admin)
    user := model.User{
        ID:           1,
        Role:         model.RoleAdmin,
        TokenVersion: 1,
    }
    // 需要先初始化 secret (从 settings 读取)
    if err := auth.InitSecret(); err != nil {
        fmt.Println("ERROR:", err)
        os.Exit(1)
    }
    token, maxAge, err := auth.GenerateJWTToken(user, -1)
    if err != nil {
        fmt.Println("ERROR:", err)
        os.Exit(1)
    }
    fmt.Printf("TOKEN=%s
MAXAGE=%d
", token, maxAge)
}
