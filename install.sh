#!/bin/bash

# Define installation directory
INSTALL_DIR="$HOME/.local/bin"
EXECUTABLE_NAME="pvr"
GITLAB_PROJECT_PATH="pantacor/pvr" # Project path on GitLab

# --- Helper Functions ---

# Function to check for jq
check_jq() {
    if ! command -v jq &> /dev/null;
then
        echo "Error: 'jq' is not installed. 'jq' is required to parse JSON responses from GitLab API."
        echo "Please install 'jq' using your system's package manager (e.g., 'sudo apt-get install jq' or 'brew install jq')."
        exit 1
    fi
}

# Function to detect OS and Architecture
detect_os_arch() {
    OS_LOWER=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH_RAW=$(uname -m)

    case "$OS_LOWER" in
        linux)
            OS="linux"
            case "$ARCH_RAW" in
                x86_64)
                    ARCH="amd64"
                    ;; 
                aarch64)
                    ARCH="arm64"
                    ;; 
                armv7l)
                    ARCH="arm"
                    ;; 
                riscv64)
                    ARCH="riscv64"
                    ;; 
                *)
                    echo "Unsupported Linux architecture: $ARCH_RAW"
                    exit 1
                    ;; 
            esac
            ARCHIVE_EXTENSION="tar.gz"
            ARCHIVE_TYPE="tar.gz"
            ;; 
        darwin)
            OS="darwin"
            case "$ARCH_RAW" in
                x86_64)
                    ARCH="amd64"
                    ;; 
                arm64) # Apple Silicon
                    ARCH="arm64"
                    ;; 
                *)
                    echo "Unsupported macOS architecture: $ARCH_RAW"
                    exit 1
                    ;; 
            esac
            ARCHIVE_EXTENSION="tar.gz"
            ARCHIVE_TYPE="tar.gz"
            ;; 
        mingw*|cygwin*|msys*) # Covers Git Bash on Windows
            OS="windows"
            case "$ARCH_RAW" in
                x86_64)
                    ARCH="amd64"
                    ;; 
                i386|i686)
                    ARCH="386"
                    ;; 
                *)
                    echo "Unsupported Windows architecture: $ARCH_RAW"
                    exit 1
                    ;; 
            esac
            ARCHIVE_EXTENSION="zip"
            ARCHIVE_TYPE="zip"
            ;; 
        *)
            echo "Unsupported operating system: $OS_LOWER"
            exit 1
            ;; 
    esac

    # For Windows, the executable name inside the zip is pvr.exe
    if [ "$OS" == "windows" ]; then
        EXECUTABLE_IN_ARCHIVE="${EXECUTABLE_NAME}.exe"
    else
        EXECUTABLE_IN_ARCHIVE="${EXECUTABLE_NAME}"
    fi
}

# --- Main Script Logic ---

check_jq
detect_os_arch

echo "Attempting to install ${EXECUTABLE_NAME} for ${OS}-${ARCH}..."

# URL-encode the project path
ENCODED_PROJECT_PATH="${GITLAB_PROJECT_PATH//	ing%2F}"

# Fetch all package versions (always fetch this first)
echo "Fetching all package versions from GitLab API..."
PACKAGES_API_URL="https://gitlab.com/api/v4/projects/${ENCODED_PROJECT_PATH}/packages?order_by=created_at&sort=desc&package_name=${EXECUTABLE_NAME}&per_page=100"
PACKAGE_INFO=$(curl -s "${PACKAGES_API_URL}")

if ! curl -s "${PACKAGES_API_URL}"; then
    echo "Error: Failed to fetch package information from GitLab API."
    exit 1
fi

# Check if PACKAGE_INFO is empty or not valid JSON
if [ -z "${PACKAGE_INFO}" ] || ! echo "${PACKAGE_INFO}" | jq -e . &> /dev/null;
then
    echo "Error: Received invalid or empty response from GitLab API."
    echo "Raw API response:"
    echo "${PACKAGE_INFO}"
    exit 1
fi

# Determine TARGET_VERSION: use provided argument or find latest stable
if [ -n "$1" ]; then
    TARGET_VERSION="$1"
    echo "Attempting to install specific version: ${TARGET_VERSION}"
    # Verify if the provided TARGET_VERSION actually exists
    if ! echo "${PACKAGE_INFO}" | jq -e ".[] | select(.version == \"${TARGET_VERSION}\")" &> /dev/null;
then
        echo "Error: Provided version '${TARGET_VERSION}' not found in GitLab package registry."
        echo "Available versions: $(echo "${PACKAGE_INFO}" | jq -r '.[].version' | sort -u | tr '\n' ' ')"
        exit 1
    fi
else
    # Extract numeric versions and find the latest
    LATEST_VERSION=$(echo "${PACKAGE_INFO}" | jq -r '.[] | select(.version | test("^[0-9]+$")) | .version' | sort -nr | head -n 1)

    if [ -z "$LATEST_VERSION" ] || [ "$LATEST_VERSION" == "null" ]; then
        echo "Error: Could not determine the latest stable version."
        echo "Please check the GitLab package registry for available versions: https://gitlab.com/${GITLAB_PROJECT_PATH}/-/packages"
        exit 1
    fi
    TARGET_VERSION="$LATEST_VERSION"
    echo "Latest stable version found: ${TARGET_VERSION}"
fi

# Construct EXPECTED_FILENAME after TARGET_VERSION is known
EXPECTED_FILENAME="${EXECUTABLE_NAME}.${TARGET_VERSION}.${OS}.${ARCH}.${ARCHIVE_EXTENSION}"

# 2. Get Package ID for the latest stable version
PACKAGE_ID=$(echo "${PACKAGE_INFO}" | jq -r ".[] | select(.version == \"${TARGET_VERSION}\") | .id")

if [ -z "$PACKAGE_ID" ] || [ "$PACKAGE_ID" == "null" ]; then
    echo "Error: Could not find package ID for version ${TARGET_VERSION}. This should not happen."
    exit 1
fi

echo "Found package ID: ${PACKAGE_ID}"

# 3. Get Package File ID for the correct OS/ARCH
echo "Fetching package file information for ${EXPECTED_FILENAME}..."
PACKAGE_FILES_API_URL="https://gitlab.com/api/v4/projects/${ENCODED_PROJECT_PATH}/packages/${PACKAGE_ID}/package_files"
PACKAGE_FILES_INFO=$(curl -s "${PACKAGE_FILES_API_URL}")

if ! curl -s "${PACKAGE_FILES_API_URL}"; then
    echo "Error: Failed to fetch package file information from GitLab API."
    exit 1
fi

# Check if PACKAGE_FILES_INFO is empty or not valid JSON
if [ -z "${PACKAGE_FILES_INFO}" ] || ! echo "${PACKAGE_FILES_INFO}" | jq -e . &> /dev/null;
then
    echo "Error: Received invalid or empty response for package files from GitLab API."
    echo "Raw API response:"
    echo "${PACKAGE_FILES_INFO}"
    exit 1
fi

FILE_ID=$(echo "${PACKAGE_FILES_INFO}" | jq -r ".[] | select(.file_name == \"${EXPECTED_FILENAME}\") | .id" | head -n 1)

if [ -z "$FILE_ID" ] || [ "$FILE_ID" == "null" ]; then
    echo "Error: Could not find file ID for ${EXPECTED_FILENAME} in package v${TARGET_VERSION}."
    echo "Available files for v${TARGET_VERSION}:"
    echo "${PACKAGE_FILES_INFO}" | jq -r '.[].file_name'
    exit 1
fi

echo "Found file ID: ${FILE_ID}"

# 4. Construct Download URL
DOWNLOAD_URL="https://gitlab.com/${GITLAB_PROJECT_PATH}/-/package_files/${FILE_ID}/download"
echo "Downloading from: ${DOWNLOAD_URL}"

# Create a temporary directory for download and extraction
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "${TEMP_DIR}"' EXIT # Clean up temp directory on exit

DOWNLOAD_PATH="${TEMP_DIR}/${EXPECTED_FILENAME}"

# Download the file
echo "Downloading ${EXPECTED_FILENAME} to ${DOWNLOAD_PATH}..."
if ! curl -L -o "${DOWNLOAD_PATH}" "${DOWNLOAD_URL}"; then
    echo "Error: Download failed."
    exit 1
fi

# Create install directory if it doesn't exist
mkdir -p "${INSTALL_DIR}"

# Extract and install
echo "Extracting and installing ${EXECUTABLE_NAME}..."
if [ "$ARCHIVE_TYPE" == "tar.gz" ]; then
    if ! tar -xzf "${DOWNLOAD_PATH}" -C "${TEMP_DIR}"; then
        echo "Error: Failed to extract archive."
        exit 1
    fi
    if ! mv "${TEMP_DIR}/${EXECUTABLE_IN_ARCHIVE}" "${INSTALL_DIR}/${EXECUTABLE_NAME}"; then
        echo "Error: Failed to move executable."
        exit 1
    fi
elif [ "$ARCHIVE_TYPE" == "zip" ]; then
    if ! unzip "${DOWNLOAD_PATH}" -d "${TEMP_DIR}"; then
        echo "Error: Failed to extract archive."
        exit 1
    fi
    if ! mv "${TEMP_DIR}/${EXECUTABLE_IN_ARCHIVE}" "${INSTALL_DIR}/${EXECUTABLE_NAME}.exe"; then
        echo "Error: Failed to move executable."
        exit 1
    fi
else
    echo "Error: Unknown archive type: ${ARCHIVE_TYPE}"
    exit 1
fi


chmod +x "${INSTALL_DIR}/${EXECUTABLE_NAME}" # This might not be needed for .exe on Windows, but harmless

echo "${EXECUTABLE_NAME} v${TARGET_VERSION} installed to ${INSTALL_DIR}/${EXECUTABLE_NAME}"

# Resolve INSTALL_DIR to its absolute path
ABS_INSTALL_DIR=$(cd "${INSTALL_DIR}" && pwd)

# Check if the absolute installation directory is in PATH
PATH_CONTAINS_INSTALL_DIR=false
IFS=':' read -ra PATH_COMPONENTS <<< "$PATH"
for component in "${PATH_COMPONENTS[@]}"; do
    if [[ "$component" == "$ABS_INSTALL_DIR" ]]; then
        PATH_CONTAINS_INSTALL_DIR=true
        break
    fi
done

if [ "$PATH_CONTAINS_INSTALL_DIR" == "false" ]; then
    echo "INFO: The installation directory '${INSTALL_DIR}' (resolved to '${ABS_INSTALL_DIR}') does not appear to be in your current shell's PATH."
    echo "To ensure '${EXECUTABLE_NAME}' can be run from any directory, you might need to add it to your PATH."
    echo "You can typically do this by adding the following line to your shell's profile (e.g., ~/.bashrc, ~/.zshrc) and then restarting your terminal or sourcing the profile file:"
    echo "export PATH=\"\$PATH:\$HOME/.local/bin\""
else
    echo "INFO: The installation directory '${INSTALL_DIR}' (resolved to '${ABS_INSTALL_DIR}') is already in your current shell's PATH."
fi

echo "Installation complete."