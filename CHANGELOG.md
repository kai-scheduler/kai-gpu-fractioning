# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Fixed
- sharingd now defaults a missing GPU-memory request or limit from the other (so `request == limit`). A container that annotates only `.request` now also gets `NVIDIA_GPU_MEMORY_LIMITS` injected (enforced at the requested size instead of being unbounded), and a container that annotates only `.limit` gets `NVIDIA_GPU_MEMORY_REQUESTS` populated. The retroactive-enforcement audit applies the same defaulting.
