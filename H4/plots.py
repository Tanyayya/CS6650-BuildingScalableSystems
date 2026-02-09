import matplotlib.pyplot as plt

num_mappers = [1, 3]
reducer_time_ms = [189, 191]

plt.figure(figsize=(7, 5))

plt.plot(
    num_mappers,
    reducer_time_ms,
    marker="o",
    markersize=8,
    linewidth=2.5
)

# Force a sensible y-range so it doesn’t squash
plt.ylim(150, 250)

plt.xticks(num_mappers)
plt.xlabel("Number of Mappers", fontsize=12)
plt.ylabel("Reducer Runtime (ms)", fontsize=12)
plt.title(
    "Reducer Runtime vs Number of Mappers\n(Reducer Does Not Scale with Parallelism)",
    fontsize=14,
    fontweight="bold",
)

plt.grid(True, axis="y", alpha=0.3)

# Annotate points
for x, y in zip(num_mappers, reducer_time_ms):
    plt.text(
        x,
        y + 6,
        f"{y} ms",
        ha="center",
        fontsize=11,
        fontweight="bold",
    )

plt.tight_layout()
plt.savefig("reducer_scaling.png", dpi=150)
plt.show()
