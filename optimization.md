# Optimization in SHEM

This document describes the optimization algorithms and strategies used in SHEM for controlling electrical devices to minimize energy costs or a more general objective function that takes into account user preferences.

Current state of this document: ideas that may or may not work in practice

## Overview
The electrical power of devices used in a household can change rapidly (on a timescale of seconds or even faster). However, the state of charge of any energy storage device will not change significantly even in several minutes. We therefore assume that a reasonable optimization result is obtained even if all variables are assumed constant within a time step of, e.g., 5 minutes. Energy prices usually do not change faster than this and this time is large enough to allow an optimization run to complete.

In certain situations it is still better to control the electrical devices on a faster timescale. Since power from the grid is usually much more expensive than power fed into the grid, it might be useful, e.g., to match the net energy demand of a household with the power discharged from battery storage as quickly as possible.

SHEM's optimization therefore operates on two timescales:
1. **Planning** (every 5 minutes): Plans device schedules for the next 24-48 hours. It uses time steps of 5 minutes and assumes that all values stay constant within one time step.
2. **Real-time control** (continuously within a 5-minute interval): Adjusts device powers based on current measurements and marginal values calculated in the planning step.

## Generalized Device Model
All controllable devices are represented using a unified three-component model. This section describes the common aspects of all models used for optimization and is not concerned with communication with the device.

### 1. Electrical Power
Each controllable device has several possible setpoints for power consumption and/or generation. The setpoints may be continuous, discrete, or a mixture of both. They can depend on other measured variables. Positive values indicate generation, negative values indicate consumption.

Examples:
- Battery storage: -5 to 5 kW (continuous)
- EV charger: 0 if no car is connected or if the car is fully charged; otherwise 0 or -4.2 kW to -[maximum power of car charger]
- PV: 0 to current generation maximum (which depends on the weather)
- Heat pump: 0 and -3.3 kW (off/on)

### 2. Energy Storage
Many controllable devices have some form of energy storage. The state of charge (SoC) is modeled here as a value between 0 and 1. The amount of energy needed to change the SoC from 0 to 1 is the intake factor, and the amount of energy (if any) that is generated when the SoC changes from 1 to 0 is the outtake factor.

Examples:
- Battery storage with 90% round-trip efficiency and a maximum charge of 10 kWh: intake factor 10 kWh / 90% = 11 kWh, outtake factor 10 kWh
- Car that needs 80 kWh to fully charge: intake factor 80 kWh, outtake factor 0
- PV with 10 kW peak: intake factor 0, outtake factor 10 kWh×time-step (as no energy storage is possible, additionally a self-discharge of 100% is applied after each time step)
- Heat pump heating: An SoC of 0 corresponds to the minimal allowed average temperature of the house, an SoC of 1 to the maximum temperature, and the intake factor depends on the outside temperature (because the heat pump is more efficient when the temperature difference is small).

Note that in some cases the SoC is not easily measured. For example, the SoC of an electric car might only be known to be less than 1 if the car is still charging and equal to 1 if it stopped charging even though it is connected and the charger would allow charging.

### 3. Storage Consumption/Production
This models the amount of energy that enters or leaves the storage. It is measured in units of the storage capacity.

Examples:
- Battery storage: 0 (or a small number accounting for self-discharge)
- Car: the depletion of SoC caused by driving the car
- PV: the energy generated in the time-step divided by the energy that would have been generated at peak conditions, minus the SoC after the last time-step (100% self-discharge)
- Heat pump: the amount of cooling the house experiences, relative to the difference of the thermal energies at max and min temperatures

## Objective
The optimization aims to minimize the expected value of a composite objective function:

Objective = Energy_Costs + Virtual_Costs

Energy costs are the costs of energy from the grid minus revenues from feeding into the grid. Additionally, the value of stored energy is taken into account. Virtual costs are a penalty for violating user requirements such as a minimum state of charge of the electric car each morning at 6:00 AM. (For details on energy prices, value of stored energy, and virtual costs, see subsections below.)

Energy costs and virtual costs are tracked separately to enable reporting actual energy cost savings.

### Energy Prices
Energy prices often are different for purchasing power from the grid and for power fed into the grid. They may change with time (often either every year or every hour).

They may consist of different components, some of which are known perfectly in advance (e.g., taxes or grid tariffs), while others are uncertain after a certain time (e.g., bulk energy prices determined by a day-ahead auction).

### Value of Stored Energy
To prevent the optimizer from depleting storage at the horizon end, we value remaining energy based on replacement costs, i.e., the value of the energy needed to recharge the storage:

Replacement_Costs = (SoC_at_Start - SoC_at_End) * Intake_Factor * Reference_Price

Replacement costs may be negative.

The reference price depends on expected conditions for the time after the optimization horizon. If the optimization horizon is 24 hours, we might consider the time 24 to 48 hours in the future. If surplus generation is expected during this time, the average feed-in price is used, and if net consumption is expected, an estimate of the purchase tariff is used. If both are expected to occur, a weighted average of the prices is used. The replacement costs may therefore depend on the forecast scenario being considered.

### Virtual Costs
Virtual costs account for user preferences. Each device can provide virtual costs that depend on its state of charge.

For example, if you want your electric car to be charged to at least 80% every morning at 6:00 AM, and you would be willing to pay up to 1 €/kWh for charging it, you can set the virtual costs of your car model accordingly.

## Forecasts and Future Values
Some optimization parameters such as PV generation or household energy demand are often not known with certainty in advance. Hence, all parameters can be modeled as stochastic variables. Since these parameters are often correlated (for example, PV generation and heat consumption both depend on the weather), the optimization considers a number of different scenarios. Each scenario contains future values for all stochastic parameters and accounts for the correlation between parameters.

Energy prices are often known in advance up to a certain horizon (e.g., for the current day and the next based on day-ahead price data). In this case, forecast scenarios are only used for the remaining time.

Some parameters change with time, but this is known well in advance. For example, grid tariffs might be different at night and during the day, but their values are known several weeks in advance.

Forecasts can depend on data from external forecasts (e.g., weather forecasts) or be extrapolated from historical values.

Some parameters are not continuously measured, for example the state of charge of an electric vehicle without online connection. In this case, the state of charge may only be known when the car is connected and is allowed to charge but does not (because it is fully charged). In such cases, even the current value is modeled as a stochastic variable.

## Optimization
This section is concerned with the optimization on a 5-minute grid. For what happens within these 5-minute slots, see [Real-Time Control](#real-time-control).

The optimization variables are the power setpoints of all controllable devices. The objective is to minimize the scenario-averaged value of the objective function.

The full optimization problem can be difficult to solve, as it may contain many variables, stochastic parameters, and a nonlinear objective function. At the same time, we need to find a reasonably good solution within a few minutes even on limited hardware like a Raspberry Pi.

We therefore use a two-step approach:
1. **Finding starting points**: The model is linearized, variables are assumed to be continuous, stochastic parameters are either replaced with their expected values or some scenarios are randomly selected, and this simplified model is solved. Then this solution (or these solutions) to the continuous model is rounded to one or more approximate solutions that respect the allowed values for power setpoints. If available, the solution found in the previous time step is adjusted to the new time horizon and SoCs and is also taken as a starting point.
2. **Simulated Annealing**: From these starting points, the full model is optimized using a simulated annealing approach. The number of starting points and the rate of change of temperature are chosen so that a (hopefully close-to-optimal) solution is found within the available time.

## Real-Time Control

Between optimization runs, a local controller adjusts device powers based on current values. This section describes the real-time control strategy.

### Reaction to Power Fluctuations
The optimal reaction to power flow fluctuations compared to forecasted and planned values can be quite complicated. Instead, we use a simple heuristic that might later be replaced by a better scheme.

#### 1. Preparation Before Real-Time Control Starts
- A small additional, fictional source with power +1 kW (and then -1 kW) is introduced in the first 5-minute slot of the optimization. The optimization is then redone, but only the simulated annealing part with a single instance, starting with the previously found solution, with a small temperature, and while disallowing power setpoints to jump over discrete steps.
- If in both cases no device in the new result adjusts its power by more than ±0.5 kW, real-time control will not react to power fluctuations during the following 5-minute slot.
- If one or more devices adjust their power by more than ±0.5 kW (sanity check that the direction is correct), the device with the largest reaction in either direction is considered the balancing device.

#### 2. Balancing During Real-Time Control
- Real-time control continually calculates the unplanned power difference ΔP. It is the sum of the difference of all uncontrolled powers (e.g., PV generation or household consumption), including the control difference of all controllable devices except for the balancing device.
- The setpoint of the balancing device is adjusted by ΔP while respecting the power limits of the balancing device.

### Reaction to Other Changes
If a large change is detected (e.g., the EV is connected or disconnected), this is immediately communicated to the optimizer that is currently concerned with everything after the current 5-minute slot, but no reaction within this time slot is taken.

### Additional Constraints
Real-time control is also responsible for ensuring certain constraints are met. For example, the grid connection may have a power limit that must be observed at all times, or grid feed-in must never exceed the current PV generation, as the energy fed in is subsidized as renewable energy. Such constraints are already observed during optimization, but they are enforced again in the last step of real-time control, directly before setpoints are sent to the devices. This ensures compliance with these constraints even if unplanned changes occur or in case of an error in the optimization results.

### Failure Detection and Fallback Strategy
Real-time control will also monitor conditions that indicate a local failure (e.g., communication loss with controllable devices or meters, or optimization did not reach a viable solution) or problems within the power grid (frequency too low or too high, rate of change of frequency too large, voltage outside allowed limits). In this case, it will slowly move all devices to a failsafe state (e.g., set battery power to zero and allow EV charging, but only with minimal power).