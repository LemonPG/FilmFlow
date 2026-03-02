#!/bin/bash

set +e

# 应用名称
appName="FilmFlow"
builtAt="$(date +'%F %T %z')"
gitAuthor="FilmFlow Contributors"

# 安全获取git提交信息
if git rev-parse --git-dir > /dev/null 2>&1; then
  gitCommit=$(git log --pretty=format:"%h" -1 2>/dev/null || echo "unknown")
else
  gitCommit="unknown"
fi


# 版本信息处理
if [ "$1" = "dev" ]; then
  version="dev"
elif [ "$1" = "beta" ]; then
  version="beta"
else
  # 获取最新的标签，如果没有标签则使用v0.0.0
  if git rev-parse --git-dir > /dev/null 2>&1; then
    version=$(git describe --abbrev=0 --tags 2>/dev/null || echo "v0.0.0")
  else
    version="v0.0.0"
  fi
fi

echo "FilmFlow version: $version"
echo "Build time: $builtAt"
echo "Git commit: $gitCommit"

# LDFlags 用于注入版本信息
ldflags="\
-w -s \
-X 'main.Version=$version' \
-X 'main.BuiltAt=$builtAt' \
-X 'main.GitCommit=$gitCommit' \
-X 'main.GitAuthor=$gitAuthor' \
"

  # 构建开发版本
  BuildDev() {
    echo "Building development version..."
    mkdir -p "dist"
    
    # 构建主要平台
    OS_ARCHES=(linux-amd64 linux-arm64 windows-amd64 windows-386 darwin-amd64 darwin-arm64)
    for os_arch in "${OS_ARCHES[@]}"; do
      os=${os_arch%%-*}
      arch=${os_arch##*-}
      suffix=""
      if [ "$os" = "windows" ]; then
        suffix=".exe"
      fi
      
      echo "building for $os-$arch"
      export GOOS=$os
      export GOARCH=$arch
      export CGO_ENABLED=1
      
      output_name="$appName-$os-$arch$suffix"
      go build -o dist/$output_name -ldflags="$ldflags" .
    done
    
    cd dist
    find . -type f -print0 | xargs -0 md5sum >md5.txt
    cat md5.txt
    cd ..
  }

# 构建发布版本
BuildRelease() {
  echo "Building release version..."
  rm -rf .git/
  mkdir -p "build"
  #echo "Building release BuildWinArm64 version..."
  #BuildWinArm64 ./build/"$appName"-windows-arm64.exe
  #echo "Building release BuildWin7 version..."
  #BuildWin7 ./build/"$appName"-windows7
  
  echo "Building release BuildOther version..."
  
  echo "Current directory: $(pwd)"

  #打印当前目录下的文件列表
  echo "Files in current directory:"
  ls -la

  # 检查go.mod文件是否存在
  if [ ! -f "go.mod" ]; then
    echo "❌ 错误：go.mod文件不存在！"
    echo "当前目录内容："
    ls -la
    exit 1
  fi

  echo "go.mod文件内容："
  head -5 go.mod

  # 设置xgo环境变量
  export XGO_IMAGE=ghcr.io/crazy-max/xgo:latest
  echo "使用XGO_IMAGE: $XGO_IMAGE"

  # 使用xgo构建
  echo "运行xgo命令..."
  xgo -out "$appName" -ldflags="$ldflags" -tags=jsoniter .
  
  # 如果xgo失败，尝试使用go直接构建作为备选方案
  # if [ $? -ne 0 ]; then
  #   echo "⚠️ xgo构建失败，尝试使用go直接构建..."
  #   echo "构建linux/amd64..."
  #   GOOS=linux GOARCH=amd64 go build -o build/"$appName"-linux-amd64 -ldflags="$ldflags" -tags=jsoniter .
  #   echo "构建windows/amd64..."
  #   GOOS=windows GOARCH=amd64 go build -o build/"$appName"-windows-amd64.exe -ldflags="$ldflags" -tags=jsoniter .
  #   echo "构建darwin/amd64..."
  #   GOOS=darwin GOARCH=amd64 go build -o build/"$appName"-darwin-amd64 -ldflags="$ldflags" -tags=jsoniter .
  # else
    # why? Because some target platforms seem to have issues with upx compression
    # upx -9 ./"$appName"-linux-amd64
    # cp ./"$appName"-windows-amd64.exe ./"$appName"-windows-amd64-upx.exe
    # upx -9 ./"$appName"-windows-amd64-upx.exe
    mv "$appName"-* build
  # fi
  
  # Build LoongArch with glibc (both old world abi1.0 and new world abi2.0)
  # Separate from musl builds to avoid cache conflicts
  #BuildLoongGLIBC ./build/$appName-linux-loong64-abi1.0 abi1.0
  #BuildLoongGLIBC ./build/$appName-linux-loong64 abi2.0
}

# 构建Docker镜像
BuildDocker() {
  echo "Building Docker image..."
  go build -o bin/"$appName" -ldflags="$ldflags" .
  
  # 如果有Dockerfile，可以构建Docker镜像
  if [ -f "Dockerfile" ]; then
    docker build -t filmflow:$version .
  fi
}

# 构建多平台Docker镜像
BuildDockerMultiplatform() {
  echo "Building multi-platform Docker images..."
  go mod download
  
  # 构建多个架构
  OS_ARCHES=(linux-amd64 linux-arm64 linux-386 linux-armv7)
  for os_arch in "${OS_ARCHES[@]}"; do
    os=${os_arch%%-*}
    arch=${os_arch##*-}
    
    echo "building for $os-$arch"
    export GOOS=$os
    export GOARCH=$arch
    export CGO_ENABLED=1
    
    if [ "$arch" = "armv7" ]; then
      export GOARCH=arm
      export GOARM=7
      arch="armv7"
    fi
    
    mkdir -p build/$os/$arch
    go build -o build/$os/$arch/"$appName" -ldflags="$ldflags" .
  done
}

# 创建发布包
MakeRelease() {
  cd build
  if [ -d compress ]; then
    rm -rv compress
  fi
  mkdir compress
  
  # 为所有构建的文件创建压缩包
  for i in $(find . -type f -name "$appName-linux-*"); do
    cp "$i" "$appName"
    tar -czvf compress/"$i".tar.gz "$appName"
    rm -f "$appName"
  done
  
  for i in $(find . -type f -name "$appName-darwin-*"); do
    cp "$i" "$appName"
    tar -czvf compress/"$i".tar.gz "$appName"
    rm -f "$appName"
  done
  
  for i in $(find . -type f -name "$appName-windows-*"); do
    cp "$i" "$appName".exe
    zip compress/$(echo $i | sed 's/\.[^.]*$//').zip "$appName".exe
    rm -f "$appName".exe
  done
  
  cd compress
  find . -type f -print0 | xargs -0 md5sum >"md5.txt"
  cat "md5.txt"
  cd ../..
}

# 构建Linux musl版本(静态链接)
BuildLinuxMusl() {
  echo "Building Linux musl versions..."
  mkdir -p "build"
  
  # 下载musl编译器
  BASE="https://github.com/OpenListTeam/musl-compilers/releases/latest/download/"
  FILES=(x86_64-linux-musl-cross aarch64-linux-musl-cross)
  for i in "${FILES[@]}"; do
    url="${BASE}${i}.tgz"
    curl -fsSL -o "${i}.tgz" "${url}"
    sudo tar xf "${i}.tgz" --strip-components 1 -C /usr/local
    rm -f "${i}.tgz"
  done
  
  OS_ARCHES=(linux-musl-amd64 linux-musl-arm64)
  CGO_ARGS=(x86_64-linux-musl-gcc aarch64-linux-musl-gcc)
  
  muslflags="--extldflags '-static -fpic' $ldflags"
  
  for i in "${!OS_ARCHES[@]}"; do
    os_arch=${OS_ARCHES[$i]}
    cgo_cc=${CGO_ARGS[$i]}
    
    echo "building for ${os_arch}"
    export GOOS=linux
    export GOARCH=${os_arch##*-}
    export CC=${cgo_cc}
    export CGO_ENABLED=1
    
    go build -o build/$appName-$os_arch -ldflags="$muslflags" .
  done
}

# 构建Windows 7兼容版本
BuildWin7() {
  echo "Building Windows 7 compatible versions..."
  
  # 设置Win7 Go编译器(如果需要)
  go_version=$(go version | grep -o 'go[0-9]\+\.[0-9]\+\.[0-9]\+' | sed 's/go//')
  echo "Detected Go version: $go_version"
  
  # 构建Windows 7兼容版本
  for arch in "386" "amd64"; do
    echo "building for windows7-${arch}"
    export GOOS=windows
    export GOARCH=${arch}
    export CGO_ENABLED=1
    
    output_name="$appName-windows7-${arch}.exe"
    go build -o "build/$output_name" -ldflags="$ldflags" .
  done
}

# 主构建逻辑
buildType=""
dockerType=""

# 解析参数
for arg in "$@"; do
  case $arg in
    dev|beta|release)
      if [ -z "$buildType" ]; then
        buildType="$arg"
      fi
      ;;
    docker|docker-multiplatform|linux_musl|win7)
      if [ -z "$dockerType" ]; then
        dockerType="$arg"
      fi
      ;;
  esac
done

# 执行构建
if [ "$buildType" = "dev" ]; then
  if [ "$dockerType" = "docker" ]; then
    BuildDocker
  elif [ "$dockerType" = "docker-multiplatform" ]; then
    BuildDockerMultiplatform
  else
    BuildDev
  fi
elif [ "$buildType" = "release" ] || [ "$buildType" = "beta" ]; then
  if [ "$dockerType" = "docker" ]; then
    BuildDocker
  elif [ "$dockerType" = "docker-multiplatform" ]; then
    BuildDockerMultiplatform
  elif [ "$dockerType" = "linux_musl" ]; then
    BuildLinuxMusl
    MakeRelease
  elif [ "$dockerType" = "win7" ]; then
    BuildWin7
    MakeRelease
  else
    BuildRelease
    MakeRelease
  fi
elif [ "$1" = "zip" ]; then
  MakeRelease
else
  echo -e "FilmFlow 构建脚本"
  echo -e "用法: $0 {dev|beta|release} [docker|docker-multiplatform|linux_musl|win7]"
  echo -e "示例:"
  echo -e "  $0 dev                    # 构建开发版本"
  echo -e "  $0 release                # 构建发布版本"
  echo -e "  $0 dev docker             # 构建Docker开发镜像"
  echo -e "  $0 release linux_musl     # 构建Linux musl静态链接版本"
  echo -e "  $0 release win7           # 构建Windows 7兼容版本"
  echo -e "  $0 zip                    # 仅打包已构建的文件"
fi

# 在Windows环境下，如果从git-bash运行，添加暂停功能
#if [[ "$OSTYPE" == "msys" ]] || [[ "$OSTYPE" == "cygwin" ]]; then
#  echo ""
#  echo "按任意键继续..."
#  read -n 1 -s
#fi
