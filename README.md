# Secure Home Energy Management (SHEM)
This project aims to design and implement a home energy management system that is reasonably secure against cyberattacks, easy to adapt, and compatible with a wide range of devices. It forecasts power consumption and PV generation, downloads energy prices and grid tariffs, uses this data to find optimal load profiles for flexible devices such as battery storage units and electric vehicle chargers, and controls those devices to follow the optimized behavior.

## Rationale
An increasing number of people use solar power, battery storage units, and electric vehicles. The flexibility of these devices can be used to lower energy costs or increase revenue, for example by optimizing against energy prices that change hourly or even every quarter hour. Some companies offer to perform this optimization for you and directly control your devices. However, if an attacker gains control of enough of these devices, they can easily overpower the existing safeguards in our power system and cause blackouts. This makes those companies and energy management systems in general attractive targets even for nation-state actors. An open-source, security-focused home energy management system that is run in a decentralized manner by many individual users might increase the overall security of our energy system. Due to the high stakes, it may be difficult to obtain a level of security that can be called "reasonable", but this project aims to achieve that goal.

## Design
SHEM follows a strict Security-by-Design approach. All device drivers, optimizers, data fetching units, and other components are small modules. Modules are controlled by an orchestrator, which monitors module behavior, restricts internet access to modules that actually need it, controls and sanitizes communication between modules, and keeps the modules up to date. Modules are isolated from each other. Care is taken to ensure that if a module is compromised, the possible consequences are minimized. This applies particularly to the possibility of coordinated attacks using many instances of SHEM, which could otherwise have consequences for the energy system as a whole. The system design is detailed in [design.md](./design.md).

## Project Status
This project is currently in the design and planning phase.

## License
This program is free software: you can redistribute it and/or modify it under the terms of the GNU Affero General Public License as published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License along with this program.  If not, see <https://www.gnu.org/licenses/>.
