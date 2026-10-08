- [ ] 
 
  **System Role & Objective:**
  You are an expert Go developer specializing in TUI (Terminal User Interface) applications using Charm's Bubble Tea framework (`github.com/charmbracelet/bubbletea`) and Lip Gloss (`github.com/charmbracelet/lipgloss`). Your goal is to write a clean, idiomatic Go program that displays real-time hardware/bluetooth connectivity status and input mapping for virtual cycling controllers.

  **Requirements:**

  1. **Bluetooth System Status Check:**
     - Display the current system Bluetooth state.
     - If Bluetooth is disabled/inactive, halt further setup steps and explicitly prompt the user to enable system Bluetooth.

  2. **Controller Status Check (Dual Controllers):**
     - Track and display the active status for both **Left Controller** and **Right Controller**.
     - If either controller is inactive/disconnected, display an explicit, individual prompt asking the user to turn on or connect that specific controller (e.g., *"Right controller disconnected: please activate the Right Controller"*).

  3. **Current Profile & Key Binding Visualizer:**
     - Display the active configuration profile name.
     - Render a formatted key mapping table specifically for the **Right SwiftClick V2** controller, mapping each controller button (e.g., Up, Down, Select, Trigger) to its corresponding keyboard stroke/emulated HID key.

  4. **Technical Constraints:**
     - Write fully functional Go code using `bubbletea` for model/update/view architecture and `lipgloss` for styling (borders, active/inactive colors, layout).
     - Include interactive toggle keys (or mock events) in `Update()` so the user can test switching Bluetooth and controller statuses dynamically.
     - Provide clear instructions to build and run the code.
